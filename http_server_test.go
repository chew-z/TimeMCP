package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"TimeMCP/internal/oauth"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// newHTTPTestHandler mirrors the production server and tool middleware wiring.
func newHTTPTestHandler(t *testing.T, cfg *Config) http.Handler {
	t.Helper()
	s := server.NewMCPServer("TimeMCP", serverVersion, server.WithToolCapabilities(true), server.WithInstructions("Time conversion and timezone utilities."))
	s.Use(loggingMiddleware(), authMiddleware(cfg))
	addTools(s, cfg)
	handler, err := buildHTTPHandler(s, cfg)
	if err != nil {
		t.Fatalf("building HTTP handler: %v", err)
	}
	return handler
}

// mcpToolRequest runs a real tool call through the HTTP handler without a socket.
func mcpToolRequest(handler http.Handler, path, authorization string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_current_time","arguments":{"timezone":"UTC"}}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-03-26")
	request.Header.Set("Origin", "https://client.example")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// assertToolResponse proves a request reached the real tool middleware, not just the transport.
func assertToolResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantAuthError bool) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("tool HTTP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Result mcp.CallToolResult `json:"result"`
		Error  json.RawMessage    `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decoding tool response: %v; body = %s", err, recorder.Body.String())
	}
	if len(response.Error) != 0 || len(response.Result.Content) == 0 || response.Result.IsError != wantAuthError {
		t.Fatalf("unexpected tool response: %s", recorder.Body.String())
	}
	if wantAuthError {
		if !strings.Contains(recorder.Body.String(), "Authentication required: missing_token") {
			t.Fatalf("legacy authentication response changed: %s", recorder.Body.String())
		}
	} else if !strings.Contains(recorder.Body.String(), "Current time in UTC") {
		t.Fatalf("tool did not execute: %s", recorder.Body.String())
	}
}

func TestHTTPHandlerOAuthDisabled(t *testing.T) {
	for _, authEnabled := range []bool{false, true} {
		name := "open"
		if authEnabled {
			name = "static auth"
		}
		t.Run(name, func(t *testing.T) {
			oauthTestEnv(t)
			cfg, err := NewConfig()
			if err != nil {
				t.Fatalf("NewConfig: %v", err)
			}
			cfg.HTTPStateless = true
			cfg.AuthEnabled = authEnabled
			cfg.AuthSecretKey = strings.Repeat("s", 32)
			handler := newHTTPTestHandler(t, cfg)
			recorder := mcpToolRequest(handler, cfg.HTTPPath, "")
			if recorder.Header().Get("WWW-Authenticate") != "" {
				t.Fatal("OAuth-off request received an OAuth challenge")
			}
			assertToolResponse(t, recorder, authEnabled)
		})
	}
}

func TestHTTPHandlerOAuthEnabled(t *testing.T) {
	validOAuthTestEnv(t)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	cfg.HTTPStateless = true
	cfg.HTTPPath = "/custom/mcp"
	cfg.HTTPCORSEnabled = true
	cfg.HTTPCORSOrigins = []string{"https://client.example"}
	handler := newHTTPTestHandler(t, cfg)
	signer := HS256Signer{Secret: cfg.AuthSecretKey, Issuer: cfg.OAuthIssuer, Audience: cfg.OAuthResource}
	oauthToken, err := signer.Sign("tester", "time", time.Hour)
	if err != nil {
		t.Fatalf("signing OAuth token: %v", err)
	}
	wrongScope, err := signer.Sign("tester", "other", time.Hour)
	if err != nil {
		t.Fatalf("signing wrong-scope token: %v", err)
	}
	signer.Secret = "another-secret"
	wrongSecret, err := signer.Sign("tester", "time", time.Hour)
	if err != nil {
		t.Fatalf("signing wrong-secret token: %v", err)
	}
	staticAuth, err := NewAuthMiddleware(cfg.AuthSecretKey, true, cfg.AuthIssuer, cfg.AuthAudience, nil)
	if err != nil {
		t.Fatalf("creating static middleware: %v", err)
	}
	staticToken, err := staticAuth.GenerateToken("1", "static-user", "user", 1)
	if err != nil {
		t.Fatalf("generating static token: %v", err)
	}
	for _, tc := range []struct {
		name, authorization string
		status              int
		challenge, body     string
	}{
		{"missing", "", http.StatusUnauthorized, "invalid_token", "unauthorized"},
		{"malformed scheme", "Basic abc", http.StatusUnauthorized, "invalid_token", "unauthorized"},
		{"malformed bearer", "Bearer", http.StatusUnauthorized, "invalid_token", "unauthorized"},
		{"malformed JWT", "Bearer nonsense", http.StatusUnauthorized, "invalid_token", "unauthorized"},
		{"static", "Bearer " + staticToken, http.StatusOK, "", ""},
		{"OAuth", "Bearer " + oauthToken, http.StatusOK, "", ""},
		{"case-insensitive bearer", "bearer " + oauthToken, http.StatusOK, "", ""},
		{"wrong scope", "Bearer " + wrongScope, http.StatusForbidden, "insufficient_scope", "insufficient_scope"},
		{"wrong secret", "Bearer " + wrongSecret, http.StatusUnauthorized, "invalid_token", "unauthorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := mcpToolRequest(handler, cfg.HTTPPath, tc.authorization)
			if recorder.Code != tc.status {
				t.Fatalf("HTTP status = %d, want %d; body = %s", recorder.Code, tc.status, recorder.Body.String())
			}
			if recorder.Header().Get("Access-Control-Allow-Origin") != "https://client.example" {
				t.Fatal("response lost allowed-origin CORS headers")
			}
			if tc.status == http.StatusOK {
				assertToolResponse(t, recorder, false)
				return
			}
			wantChallenge := `Bearer realm="time-mcp", error="` + tc.challenge + `", resource_metadata="https://karma.example/.well-known/oauth-protected-resource/time/mcp", scope="time"`
			if got := recorder.Header().Get("WWW-Authenticate"); got != wantChallenge {
				t.Fatalf("challenge = %q, want %q", got, wantChallenge)
			}
			if recorder.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(recorder.Body.String()) != `{"error":"`+tc.body+`"}` {
				t.Fatalf("unexpected error response: %s", recorder.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		method, path, origin string
		status               int
	}{
		{http.MethodOptions, cfg.HTTPPath, "https://client.example", http.StatusOK},
		{http.MethodGet, "/health", "", http.StatusOK},
		{http.MethodGet, "/.well-known/oauth-protected-resource/time/mcp", "", http.StatusOK},
		{http.MethodGet, "/.well-known/oauth-authorization-server/time", "", http.StatusOK},
		{http.MethodGet, "/authorize", "", http.StatusBadRequest},
		{http.MethodPost, "/token", "", http.StatusUnauthorized},
		{http.MethodPost, "/register", "", http.StatusBadRequest},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Origin", tc.origin)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.status || strings.HasPrefix(recorder.Header().Get("WWW-Authenticate"), "Bearer ") {
				t.Fatalf("public route/preflight status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	// Preflight bypass also works without the CORS wrapper handling the request.
	req := httptest.NewRequest(http.MethodOptions, cfg.HTTPPath, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
		t.Fatal("OPTIONS without Origin was rejected by OAuth guard")
	}
}

func TestValidateJWTOAuthProfiles(t *testing.T) {
	oc := &oauth.Config{Issuer: "https://karma.example/time", Resource: "https://karma.example/time/mcp", Scope: "time", Realm: "time-mcp"}
	secret := strings.Repeat("s", 32)
	static, err := NewAuthMiddleware(secret, true, "TimeMCP", "TimeMCP-user", nil)
	if err != nil {
		t.Fatalf("creating static middleware: %v", err)
	}
	dual, err := NewAuthMiddleware(secret, true, "TimeMCP", "TimeMCP-user", oc)
	if err != nil {
		t.Fatalf("creating dual-profile middleware: %v", err)
	}
	signer := HS256Signer{Secret: secret, Issuer: oc.Issuer, Audience: oc.Resource}
	token, err := signer.Sign("tester", "time", time.Hour)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	if _, err := static.validateJWT(token); err == nil {
		t.Fatal("static-only validator accepted OAuth profile")
	}
	if _, err := dual.validateJWT(token); err != nil {
		t.Fatalf("dual-profile validator rejected OAuth token: %v", err)
	}
	staticToken, err := static.GenerateToken("1", "static-user", "user", 1)
	if err != nil {
		t.Fatalf("generating static token: %v", err)
	}
	if _, err := dual.validateJWT(staticToken); err != nil {
		t.Fatalf("dual-profile validator rejected static token: %v", err)
	}
	// Static tokens retain optional expiration; enabling OAuth must not tighten that profile.
	staticClaims := Claims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "TimeMCP", Audience: jwt.ClaimStrings{"TimeMCP-user"}}, UserID: "1", Username: "static-user", Role: "user"}
	noExpiry, err := jwt.NewWithClaims(jwt.SigningMethodHS256, staticClaims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing static token without expiration: %v", err)
	}
	if _, err := dual.validateJWT(noExpiry); err != nil {
		t.Fatalf("static expiration policy changed: %v", err)
	}
	// Explicitly check the request context and unchanged tool-auth layer.
	cfg := &Config{AuthEnabled: true, AuthSecretKey: secret, AuthIssuer: "TimeMCP", AuthAudience: "TimeMCP-user"}
	contextFunc, err := createHTTPMiddleware(cfg, oc)
	if err != nil {
		t.Fatalf("creating context middleware: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx := contextFunc(context.Background(), req)
	if !isAuthenticated(ctx) || getAuthError(ctx) != "" || checkAuth(ctx, "get_current_time", cfg) != nil {
		t.Fatal("OAuth token failed the context/tool authentication layer")
	}
	id, name, role := getUserInfo(ctx)
	if id != "tester" || name != "tester" || role != oauthRole {
		t.Fatal("OAuth identity was not stored in the request context")
	}
	for _, tc := range []struct {
		name, issuer, audience, scope, role string
		expiration                          *jwt.NumericDate
		method                              jwt.SigningMethod
		wantErr                             error
	}{
		{"missing expiration", oc.Issuer, oc.Resource, "time", "oauth", nil, jwt.SigningMethodHS256, jwt.ErrTokenRequiredClaimMissing},
		{"wrong scope", oc.Issuer, oc.Resource, "wrong", "oauth", jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS256, errInsufficientScope},
		{"missing scope", oc.Issuer, oc.Resource, "", "oauth", jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS256, errInsufficientScope},
		{"expired OAuth", oc.Issuer, oc.Resource, "time", "oauth", jwt.NewNumericDate(time.Now().Add(-2 * time.Minute)), jwt.SigningMethodHS256, jwt.ErrTokenExpired},
		{"expired static", "TimeMCP", "TimeMCP-user", "", "user", jwt.NewNumericDate(time.Now().Add(-2 * time.Minute)), jwt.SigningMethodHS256, jwt.ErrTokenExpired},
		{"wrong audience", oc.Issuer, "other", "time", "oauth", jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS256, jwt.ErrTokenInvalidAudience},
		{"wrong issuer", "https://other.example", oc.Resource, "time", "oauth", jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS256, jwt.ErrTokenInvalidIssuer},
		{"wrong algorithm", oc.Issuer, oc.Resource, "time", "oauth", jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS384, jwt.ErrTokenSignatureInvalid},
		{"missing identity", oc.Issuer, oc.Resource, "time", "", jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS256, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := Claims{RegisteredClaims: jwt.RegisteredClaims{Issuer: tc.issuer, Audience: jwt.ClaimStrings{tc.audience}, ExpiresAt: tc.expiration}, UserID: "tester", Username: "tester", Role: tc.role, Scope: tc.scope}
			signed, err := jwt.NewWithClaims(tc.method, claims).SignedString([]byte(secret))
			if err != nil {
				t.Fatalf("signing token: %v", err)
			}
			if _, err := dual.validateJWT(signed); err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("validation error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
