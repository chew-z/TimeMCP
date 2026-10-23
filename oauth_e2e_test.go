package main

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	json "encoding/json/v2"

	"TimeMCP/internal/oauth"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// testSecret is the ≥32-byte signing secret shared by the OAuth tests.
const testSecret = "this-secret-is-longer-than-thirty-two-bytes"

// oauthVerifier is the PKCE verifier; its S256 challenge is computed per flow.
const oauthVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

func oauthRequest(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func oauthDo(t *testing.T, httpClient *http.Client, req *http.Request) (int, http.Header, string) {
	t.Helper()
	response, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("closing OAuth response: %v", err)
		}
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, string(body)
}

// oauthHiddenField extracts a hidden form input from the login page HTML.
func oauthHiddenField(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("missing %s in %q", name, body)
	}
	value := body[start+len(marker):]
	end := strings.IndexByte(value, '"')
	if end < 0 {
		t.Fatalf("unterminated %s", name)
	}
	return value[:end]
}

// testOAuthMCPConfig builds OAuth settings with a real password hash and no environment dependencies.
func testOAuthMCPConfig(t *testing.T) *Config {
	t.Helper()
	hash, err := oauth.HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return &Config{
		AuthEnabled:            true,
		AuthSecretKey:          testSecret,
		AuthIssuer:             "TimeMCP",
		AuthAudience:           "TimeMCP-user",
		HTTPPath:               "/mcp",
		OAuthEnabled:           true,
		OAuthLoginUser:         "tester",
		OAuthLoginPasswordHash: hash,
		OAuthAccessTTL:         time.Hour,
		OAuthRefreshTTL:        720 * time.Hour,
	}
}

// oauthServer starts a local server before deriving its public OAuth URLs.
// Atomic publication keeps the handler indirection safe across HTTP goroutines.
func oauthServer(t *testing.T) (*httptest.Server, *Config) {
	t.Helper()
	var handler atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.Load().(http.Handler).ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := testOAuthMCPConfig(t)
	cfg.OAuthIssuer = srv.URL
	cfg.OAuthResource = srv.URL + "/mcp"
	handler.Store(newHTTPTestHandler(t, cfg))
	return srv, cfg
}

func oauthRegister(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	body := `{"client_name":"test client","redirect_uris":["https://example.test/callback"],"token_endpoint_auth_method":"none"}`
	req := oauthRequest(t, http.MethodPost, srv.URL+"/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	status, _, response := oauthDo(t, http.DefaultClient, req)
	if status != http.StatusCreated {
		t.Fatalf("register = %d: %s", status, response)
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(response), &registered); err != nil || registered.ClientID == "" {
		t.Fatalf("registration = %s: %v", response, err)
	}
	return registered.ClientID
}

// obtainOAuthTokens walks the full browser flow: authorize (login form) →
// login → authorization code → token exchange.
func obtainOAuthTokens(t *testing.T, srv *httptest.Server, cfg *Config, clientID string) oauth.TokenResponse {
	t.Helper()
	sum := sha256.Sum256([]byte(oauthVerifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://example.test/callback"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"state":                 {"state"},
	}
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status, _, body := oauthDo(t, httpClient, oauthRequest(t, http.MethodGet, srv.URL+"/authorize?"+query.Encode(), nil))
	if status != http.StatusOK {
		t.Fatalf("authorize = %d: %s", status, body)
	}
	form := url.Values{
		"pending_id": {oauthHiddenField(t, body, "pending_id")},
		"csrf_token": {oauthHiddenField(t, body, "csrf_token")},
		"username":   {cfg.OAuthLoginUser},
		"password":   {"pw"},
	}
	login := oauthRequest(t, http.MethodPost, srv.URL+"/login", strings.NewReader(form.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, header, body := oauthDo(t, httpClient, login)
	if status != http.StatusFound {
		t.Fatalf("login = %d: %s", status, body)
	}
	redirect, err := url.Parse(header.Get("Location"))
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("redirect = %q, %v", header.Get("Location"), err)
	}
	if redirect.Scheme != "https" || redirect.Host != "example.test" || redirect.Path != "/callback" || redirect.Query().Get("state") != "state" {
		t.Fatalf("unexpected authorization redirect: %s", redirect)
	}
	form = url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {redirect.Query().Get("code")},
		"redirect_uri":  {"https://example.test/callback"},
		"client_id":     {clientID},
		"code_verifier": {oauthVerifier},
	}
	token := oauthRequest(t, http.MethodPost, srv.URL+"/token", strings.NewReader(form.Encode()))
	token.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, _, body = oauthDo(t, httpClient, token)
	if status != http.StatusOK {
		t.Fatalf("token = %d: %s", status, body)
	}
	var tokens oauth.TokenResponse
	if err := json.Unmarshal([]byte(body), &tokens); err != nil || tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.TokenType != "Bearer" || tokens.Scope != "time" {
		t.Fatalf("token response = %s: %v", body, err)
	}
	return tokens
}

// TestEndToEndOAuthThenTools walks discovery → registration → browser login →
// tokens → authenticated MCP use → refresh → static-JWT fallback → challenge.
func TestEndToEndOAuthThenTools(t *testing.T) {
	srv, cfg := oauthServer(t)
	clientID := oauthRegister(t, srv)
	tokens := obtainOAuthTokens(t, srv, cfg, clientID)
	useToolsWithBearer(t, srv.URL+"/mcp", tokens.AccessToken)

	refresh := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	}
	req := oauthRequest(t, http.MethodPost, srv.URL+"/token", strings.NewReader(refresh.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, _, body := oauthDo(t, http.DefaultClient, req)
	if status != http.StatusOK {
		t.Fatalf("refresh = %d: %s", status, body)
	}
	var renewed oauth.TokenResponse
	if err := json.Unmarshal([]byte(body), &renewed); err != nil || renewed.AccessToken == "" || renewed.RefreshToken == "" || renewed.RefreshToken == tokens.RefreshToken || renewed.Scope != "time" {
		t.Fatalf("refresh response = %s: %v", body, err)
	}
	useToolsWithBearer(t, srv.URL+"/mcp", renewed.AccessToken)

	staticAuth, err := NewAuthMiddleware(cfg.AuthSecretKey, true, cfg.AuthIssuer, cfg.AuthAudience, nil)
	if err != nil {
		t.Fatalf("creating static auth: %v", err)
	}
	staticToken, err := staticAuth.GenerateToken("admin", "admin", "user", 1)
	if err != nil {
		t.Fatal(err)
	}
	useToolsWithBearer(t, srv.URL+"/mcp", staticToken)

	status, header, _ := oauthDo(t, http.DefaultClient, oauthRequest(t, http.MethodPost, srv.URL+"/mcp", nil))
	if status != http.StatusUnauthorized || !strings.Contains(header.Get("WWW-Authenticate"), `resource_metadata="`+srv.URL+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("missing token = %d, %q", status, header.Get("WWW-Authenticate"))
	}
}

// TestOAuthDiscoveryDocuments verifies JSON metadata on local and reverse-proxy paths.
func TestOAuthDiscoveryDocuments(t *testing.T) {
	srv, cfg := oauthServer(t)
	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-authorization-server",
	} {
		t.Run(path, func(t *testing.T) {
			status, header, body := oauthDo(t, srv.Client(), oauthRequest(t, http.MethodGet, srv.URL+path, nil))
			if status != http.StatusOK || !strings.HasPrefix(header.Get("Content-Type"), "application/json") {
				t.Fatalf("metadata = %d, %q: %s", status, header.Get("Content-Type"), body)
			}
			assertOAuthDiscovery(t, path, body, cfg.OAuthIssuer, cfg.OAuthResource)
		})
	}
	// Retain Answer's karma-shaped path-insertion coverage, adapted to /time.
	t.Run("reverse proxy prefix", func(t *testing.T) {
		cfg := testOAuthMCPConfig(t)
		cfg.OAuthIssuer = "https://karma.example/time"
		cfg.OAuthResource = cfg.OAuthIssuer + "/mcp"
		handler := newHTTPTestHandler(t, cfg)
		for _, path := range []string{
			"/.well-known/oauth-protected-resource/time/mcp",
			"/.well-known/oauth-authorization-server/time",
			"/.well-known/oauth-protected-resource",
			"/.well-known/oauth-authorization-server",
		} {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, body = %s", path, recorder.Code, recorder.Body.String())
			}
			assertOAuthDiscovery(t, path, recorder.Body.String(), cfg.OAuthIssuer, cfg.OAuthResource)
		}
	})
}

// assertOAuthDiscovery checks the advertised URLs and scope in either metadata document.
func assertOAuthDiscovery(t *testing.T, path, body, issuer, resource string) {
	t.Helper()
	if strings.Contains(path, "oauth-protected-resource") {
		var metadata oauth.ProtectedResourceMetadata
		if err := json.Unmarshal([]byte(body), &metadata); err != nil {
			t.Fatalf("decoding resource metadata: %v", err)
		}
		if metadata.Resource != resource || !slices.Equal(metadata.AuthorizationServers, []string{issuer}) || !slices.Equal(metadata.ScopesSupported, []string{"time"}) {
			t.Fatalf("incorrect resource metadata: %s", body)
		}
		return
	}
	var metadata oauth.AuthorizationServerMetadata
	if err := json.Unmarshal([]byte(body), &metadata); err != nil {
		t.Fatalf("decoding authorization metadata: %v", err)
	}
	if metadata.Issuer != issuer || metadata.AuthorizationEndpoint != issuer+"/authorize" || metadata.TokenEndpoint != issuer+"/token" || metadata.RegistrationEndpoint != issuer+"/register" || !slices.Equal(metadata.ScopesSupported, []string{"time"}) {
		t.Fatalf("incorrect authorization metadata: %s", body)
	}
}

// useToolsWithBearer initializes a real MCP client, lists all TimeMCP tools, and calls one.
func useToolsWithBearer(t *testing.T, endpoint, bearer string) {
	t.Helper()
	mcpClient, err := client.NewStreamableHttpClient(endpoint, transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + bearer}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mcpClient.Close(); err != nil {
			t.Errorf("closing MCP client: %v", err)
		}
	})
	if err := mcpClient.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpClient.Initialize(t.Context(), mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION, ClientInfo: mcpgo.Implementation{Name: "test", Version: "0"}}}); err != nil {
		t.Fatal(err)
	}
	tools, err := mcpClient.ListTools(t.Context(), mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"convert_time", "describe_time", "get_current_time"}) {
		t.Fatalf("unexpected tools: %v", names)
	}
	var request mcpgo.CallToolRequest
	request.Params.Name = "get_current_time"
	request.Params.Arguments = map[string]any{"timezone": "UTC"}
	result, err := mcpClient.CallTool(t.Context(), request)
	if err != nil {
		t.Fatalf("calling get_current_time: %v", err)
	}
	if result.IsError || len(result.Content) == 0 {
		t.Fatalf("tool returned an error or no content: %+v", result)
	}
	text, ok := result.Content[0].(mcpgo.TextContent)
	if !ok || !strings.Contains(text.Text, "Current time in UTC") {
		t.Fatalf("unexpected get_current_time output: %+v", result.Content)
	}
}
