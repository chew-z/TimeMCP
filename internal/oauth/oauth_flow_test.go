package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	flowUser     = "tester"
	flowPassword = "correct horse battery staple"
	flowVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

type signCall struct {
	username string
	scope    string
	ttl      time.Duration
}

type testSigner struct {
	issuer, audience string
	calls            []signCall
}

func (s *testSigner) Sign(username, scope string, ttl time.Duration) (string, error) {
	s.calls = append(s.calls, signCall{username: username, scope: scope, ttl: ttl})
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": username, "scope": scope, "iss": s.issuer, "aud": s.audience,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(ttl).Unix(),
	}).SignedString([]byte("test-secret"))
}

type flowHarness struct {
	cfg    Config
	store  *Store
	client *Client
	auth   *AuthorizationService
	tokens *TokenService
	signer *testSigner
}

func newFlowHarness(t *testing.T) *flowHarness {
	t.Helper()
	hash, err := HashPassword(flowPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cfg := Config{
		Issuer: "https://example.test/time", Resource: "https://example.test/time/mcp",
		LoginUser: flowUser, LoginPasswordHash: hash, AccessTokenTTL: time.Hour,
		RefreshTokenTTL: 720 * time.Hour, AuthCodeTTL: time.Minute, Scope: "time", Realm: "time-mcp",
	}
	signer := &testSigner{issuer: cfg.Issuer, audience: cfg.Resource}
	cfg.Signer = signer
	store := NewStore(nil)
	client := &Client{
		ClientID: "desktop-client", RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
		ClientName: "Claude Desktop", TokenEndpointAuthMethod: "none", CreatedAt: time.Now(),
	}
	if err := store.RegisterClient(client); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	registry := NewClientRegistry(store, slog.Default())
	return &flowHarness{
		cfg: cfg, store: store, client: client, signer: signer,
		auth:   NewAuthorizationService(cfg, store, registry, slog.Default()),
		tokens: NewTokenService(cfg, store, slog.Default()),
	}
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func hiddenField(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("login form has no %s field", name)
	}
	value := body[start+len(marker):]
	end := strings.IndexByte(value, '"')
	if end < 0 {
		t.Fatalf("login form field %s is unterminated", name)
	}
	return value[:end]
}

func (h *flowHarness) authorize(t *testing.T, overrides map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	query := url.Values{
		"response_type": {"code"}, "client_id": {h.client.ClientID}, "redirect_uri": {h.client.RedirectURIs[0]},
		"code_challenge": {challengeFor(flowVerifier)}, "code_challenge_method": {"S256"}, "state": {"opaque-state"},
	}
	for key, value := range overrides {
		if value == "" {
			query.Del(key)
			continue
		}
		query.Set(key, value)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/authorize?"+query.Encode(), nil)
	request.RemoteAddr = "203.0.113.20:1234"
	response := httptest.NewRecorder()
	h.auth.AuthorizeHandler()(response, request)
	return response
}

func (h *flowHarness) login(t *testing.T, pendingID, csrf, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"pending_id": {pendingID}, "csrf_token": {csrf}, "username": {username}, "password": {password}}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "203.0.113.21:1234"
	response := httptest.NewRecorder()
	h.auth.LoginHandler()(response, request)
	return response
}

func redirectQuery(t *testing.T, response *httptest.ResponseRecorder) url.Values {
	t.Helper()
	target, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing redirect: %v", err)
	}
	return target.Query()
}

func (h *flowHarness) codeFromHappyPath(t *testing.T, state string) (string, url.Values) {
	t.Helper()
	authorized := h.authorize(t, map[string]string{"state": state})
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorize status = %d: %s", authorized.Code, authorized.Body.String())
	}
	body := authorized.Body.String()
	login := h.login(t, hiddenField(t, body, "pending_id"), hiddenField(t, body, "csrf_token"), flowUser, flowPassword)
	if login.Code != http.StatusFound {
		t.Fatalf("login status = %d: %s", login.Code, login.Body.String())
	}
	query := redirectQuery(t, login)
	return query.Get("code"), query
}

func (h *flowHarness) token(t *testing.T, form url.Values) (*httptest.ResponseRecorder, TokenResponse) {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "203.0.113.22:1234"
	response := httptest.NewRecorder()
	h.tokens.TokenHandler()(response, request)
	var tokens TokenResponse
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &tokens); err != nil {
			t.Fatalf("decoding token response: %v", err)
		}
	}
	return response, tokens
}

func (h *flowHarness) codeExchangeForm(code string) url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {h.client.RedirectURIs[0]},
		"client_id": {h.client.ClientID}, "code_verifier": {flowVerifier},
	}
}

func TestAuthorizeHandlerIsRateLimited(t *testing.T) {
	h := newFlowHarness(t)
	handler := h.auth.AuthorizeHandler()

	limited := false
	for i := 0; i < rateLimitCapacity+2; i++ {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/authorize", nil)
		req.RemoteAddr = "203.0.113.23:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("authorization requests must be rate limited per IP")
	}
}

func TestLoginHandlerIsRateLimited(t *testing.T) {
	h := newFlowHarness(t)
	handler := h.auth.LoginHandler()

	limited := false
	for i := 0; i < rateLimitCapacity+2; i++ {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.24:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("login requests must be rate limited per IP")
	}
}

func TestTokenHandlerIsRateLimited(t *testing.T) {
	h := newFlowHarness(t)
	handler := h.tokens.TokenHandler()

	limited := false
	for i := 0; i < rateLimitCapacity+2; i++ {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/token", strings.NewReader("grant_type=authorization_code"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.25:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("token requests must be rate limited per IP")
	}
}

func TestFullAuthorizationCodeFlow(t *testing.T) {
	h := newFlowHarness(t)
	code, query := h.codeFromHappyPath(t, "opaque-state")
	if code == "" || query.Get("state") != "opaque-state" {
		t.Fatalf("authorization response = %v", query)
	}
	response, tokens := h.token(t, h.codeExchangeForm(code))
	if response.Code != http.StatusOK || tokens.TokenType != "Bearer" || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("token response = %d %+v", response.Code, tokens)
	}
	if len(h.signer.calls) != 1 || h.signer.calls[0] != (signCall{flowUser, "time", h.cfg.AccessTokenTTL}) {
		t.Fatalf("signer calls = %+v", h.signer.calls)
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tokens.AccessToken, claims, func(*jwt.Token) (any, error) { return []byte("test-secret"), nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || claims["username"] != flowUser || claims["scope"] != "time" || claims["iss"] != h.cfg.Issuer || claims["aud"] != h.cfg.Resource {
		t.Fatalf("JWT = %#v, err = %v", claims, err)
	}
}

func TestAuthorizeRejectsPlainPKCE(t *testing.T) {
	h := newFlowHarness(t)
	response := h.authorize(t, map[string]string{"code_challenge_method": "plain"})
	if response.Code != http.StatusFound || redirectQuery(t, response).Get("error") != "invalid_request" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestAuthorizeValidation(t *testing.T) {
	h := newFlowHarness(t)
	tests := []struct {
		name, key, value, wantError string
		page                        bool
	}{
		{"unknown client", "client_id", "unknown", "", true},
		{"unregistered redirect", "redirect_uri", "https://evil.test/callback", "", true},
		{"missing challenge", "code_challenge", "", "invalid_request", false},
		{"wrong response type", "response_type", "token", "unsupported_response_type", false},
		{"foreign resource", "resource", "https://other.test/mcp", "invalid_target", false},
		{"foreign scope", "scope", "admin", "invalid_scope", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := h.authorize(t, map[string]string{test.key: test.value})
			if test.page {
				if response.Code != http.StatusBadRequest || response.Header().Get("Location") != "" {
					t.Fatalf("response = %d location=%q", response.Code, response.Header().Get("Location"))
				}
				return
			}
			if response.Code != http.StatusFound {
				t.Fatalf("status = %d", response.Code)
			}
			query := redirectQuery(t, response)
			if query.Get("error") != test.wantError || query.Get("state") != "opaque-state" || query.Get("iss") != h.cfg.Issuer {
				t.Fatalf("error response = %v", query)
			}
		})
	}
}

func TestStateSurvivesUntouched(t *testing.T) {
	for _, state := range []string{"plain", "with spaces", "amp&equals=signs", "hash#fragment", "unicode-ąęśćż", "plus+and%20percent"} {
		t.Run(state, func(t *testing.T) {
			_, query := newFlowHarness(t).codeFromHappyPath(t, state)
			if query.Get("state") != state {
				t.Fatalf("state = %q, want %q", query.Get("state"), state)
			}
		})
	}
}

func TestAuthorizationResponseCarriesIssuer(t *testing.T) {
	h := newFlowHarness(t)
	_, query := h.codeFromHappyPath(t, "state")
	if query.Get("iss") != h.cfg.Issuer {
		t.Fatalf("iss = %q, want %q", query.Get("iss"), h.cfg.Issuer)
	}
}

func TestLoginRejectsBadCSRF(t *testing.T) {
	h := newFlowHarness(t)
	authorized := h.authorize(t, nil)
	id := hiddenField(t, authorized.Body.String(), "pending_id")
	response := h.login(t, id, "bad", flowUser, flowPassword)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	if _, ok := h.store.GetPending(id); ok {
		t.Fatal("bad CSRF left the pending authorization live")
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	for _, test := range []struct{ name, username, password string }{
		{"wrong password", flowUser, "not-the-password"},
		{"wrong username", "other", flowPassword},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newFlowHarness(t)
			authorized := h.authorize(t, nil)
			body := authorized.Body.String()
			response := h.login(t, hiddenField(t, body, "pending_id"), hiddenField(t, body, "csrf_token"), test.username, test.password)
			if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), test.password) {
				t.Fatalf("response = %d; body leaks password=%t", response.Code, strings.Contains(response.Body.String(), test.password))
			}
		})
	}
}

func TestLoginHandlerBoundsConcurrentVerification(t *testing.T) {
	h := newFlowHarness(t)
	authorized := h.authorize(t, nil)
	body := authorized.Body.String()
	id, csrf := hiddenField(t, body, "pending_id"), hiddenField(t, body, "csrf_token")

	release := make(chan struct{})
	var inside sync.WaitGroup
	inside.Add(maxConcurrentLogins)
	h.auth.verifyPassword = func(_, _ string) bool {
		inside.Done()
		<-release
		return false
	}

	var finished sync.WaitGroup
	finished.Add(maxConcurrentLogins)
	for range maxConcurrentLogins {
		go func() {
			defer finished.Done()
			h.login(t, id, csrf, flowUser, "whatever")
		}()
	}
	inside.Wait()

	response := h.login(t, id, csrf, flowUser, "whatever")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	pending, ok := h.store.GetPending(id)
	if !ok {
		t.Fatal("busy login consumed the pending authorization")
	}
	if pending.FailedLogins != 0 {
		t.Fatalf("busy login failures = %d, want 0", pending.FailedLogins)
	}

	close(release)
	finished.Wait()

	inside.Add(1)
	response = h.login(t, id, csrf, flowUser, "whatever")
	if response.Code == http.StatusServiceUnavailable {
		t.Fatal("login stayed busy after verification slots were released")
	}
	inside.Wait()
}

func TestLoginLockoutDropsPendingAuthorization(t *testing.T) {
	h := newFlowHarness(t)
	authorized := h.authorize(t, nil)
	body := authorized.Body.String()
	id, csrf := hiddenField(t, body, "pending_id"), hiddenField(t, body, "csrf_token")
	for attempt := 0; attempt < maxLoginAttempts; attempt++ {
		response := h.login(t, id, csrf, flowUser, "wrong")
		if attempt < maxLoginAttempts-1 && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", attempt, response.Code)
		}
	}
	if _, ok := h.store.GetPending(id); ok {
		t.Fatal("lockout left pending authorization live")
	}
}

func TestTokenExchangeValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"wrong verifier", func(form url.Values) { form.Set("code_verifier", "not-the-verifier") }},
		{"absent verifier", func(form url.Values) { form.Del("code_verifier") }},
		{"mismatched redirect URI", func(form url.Values) { form.Set("redirect_uri", "https://claude.ai/other") }},
		{"unknown code", func(form url.Values) { form.Set("code", "fabricated") }},
		{"foreign resource", func(form url.Values) { form.Set("resource", "https://other.example/mcp") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newFlowHarness(t)
			code, _ := h.codeFromHappyPath(t, "state")
			form := h.codeExchangeForm(code)
			test.mutate(form)
			response, _ := h.token(t, form)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding response: %v", err)
			}
			if body["error"] != "invalid_grant" {
				t.Fatalf("error = %q, want invalid_grant", body["error"])
			}
		})
	}
}

func TestAuthorizationCodeIsSingleUse(t *testing.T) {
	h := newFlowHarness(t)
	code, _ := h.codeFromHappyPath(t, "state")
	h.token(t, h.codeExchangeForm(code))
	response, _ := h.token(t, h.codeExchangeForm(code))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthorizationCodeBoundToItsClient(t *testing.T) {
	h := newFlowHarness(t)
	other := &Client{
		ClientID:                "other-client",
		RedirectURIs:            h.client.RedirectURIs,
		TokenEndpointAuthMethod: "none",
		CreatedAt:               time.Now(),
	}
	if err := h.store.RegisterClient(other); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	code, _ := h.codeFromHappyPath(t, "state")
	form := h.codeExchangeForm(code)
	form.Set("client_id", other.ClientID)
	response, _ := h.token(t, form)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	h := newFlowHarness(t)
	code, _ := h.codeFromHappyPath(t, "state")
	_, first := h.token(t, h.codeExchangeForm(code))
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {h.client.ClientID}}
	response, successor := h.token(t, form)
	if response.Code != http.StatusOK || successor.RefreshToken == "" || successor.RefreshToken == first.RefreshToken {
		t.Fatalf("rotation response = %d %+v", response.Code, successor)
	}
	reused, _ := h.token(t, form)
	if reused.Code != http.StatusBadRequest {
		t.Fatalf("reused predecessor status = %d", reused.Code)
	}
	form.Set("refresh_token", successor.RefreshToken)
	dead, _ := h.token(t, form)
	if dead.Code != http.StatusBadRequest {
		t.Fatalf("successor status after reuse = %d", dead.Code)
	}
}

func TestTokenEndpointClientAuthentication(t *testing.T) {
	h := newFlowHarness(t)
	confidential := &Client{ClientID: "confidential", ClientSecret: "secret", RedirectURIs: h.client.RedirectURIs, TokenEndpointAuthMethod: "client_secret_basic", CreatedAt: time.Now()}
	if err := h.store.RegisterClient(confidential); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	tests := []struct {
		name, clientID, secret, method string
		want                           int
	}{
		{"public presents secret", h.client.ClientID, "secret", "post", http.StatusUnauthorized},
		{"confidential basic succeeds", confidential.ClientID, confidential.ClientSecret, "basic", http.StatusBadRequest},
		{"confidential method mismatch", confidential.ClientID, confidential.ClientSecret, "post", http.StatusUnauthorized},
		{"confidential wrong secret", confidential.ClientID, "not-the-secret", "basic", http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{"grant_type": {"unsupported"}, "client_id": {test.clientID}, "client_secret": {test.secret}}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/token", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if test.method == "basic" {
				request.SetBasicAuth(test.clientID, test.secret)
			}
			response := httptest.NewRecorder()
			h.tokens.TokenHandler()(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
			if test.want == http.StatusUnauthorized {
				var body map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("decoding response: %v", err)
				}
				if body["error"] != "invalid_client" {
					t.Fatalf("error = %q, want invalid_client", body["error"])
				}
			}
		})
	}
}

func TestUnsupportedGrantType(t *testing.T) {
	h := newFlowHarness(t)
	response, _ := h.token(t, url.Values{"grant_type": {"password"}, "client_id": {h.client.ClientID}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body["error"] != "unsupported_grant_type" {
		t.Fatalf("error = %q, want unsupported_grant_type", body["error"])
	}
}

func TestVerifyPKCE(t *testing.T) {
	challenge := challengeFor(flowVerifier)
	if !verifyPKCE(challenge, flowVerifier) {
		t.Fatal("correct verifier was rejected")
	}
	for _, verifier := range []string{"", "wrong", flowVerifier + "x"} {
		if verifyPKCE(challenge, verifier) {
			t.Fatalf("verifier %q was accepted", verifier)
		}
	}
	if verifyPKCE("", flowVerifier) {
		t.Fatal("empty challenge was accepted")
	}
}
