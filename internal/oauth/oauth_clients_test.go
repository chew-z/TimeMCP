package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T) (*ClientRegistry, *Store) {
	t.Helper()
	store, _ := newTestStore()
	return NewClientRegistry(store, slog.Default()), store
}

// rewriteHostTransport points every outbound request at a local test server
// while leaving the request URL — and therefore the CIMD client_id — intact.
// It bypasses the guarded dialer on purpose, so tests can exercise document
// validation separately from the address guard.
type rewriteHostTransport struct {
	target string
}

func (t *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = t.target
	return http.DefaultTransport.RoundTrip(clone)
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false}, // cloud metadata service
		{"0.0.0.0", false},
		{"224.0.0.1", false},
		{"100.64.0.1", false}, // carrier-grade NAT
		{"fd00::1", false},    // IPv6 unique local
		{"fe80::1", false},    // IPv6 link-local
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
	}

	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("could not parse %q", tc.ip)
			}
			if got := isPublicIP(ip); got != tc.public {
				t.Errorf("isPublicIP(%s) = %v, want %v", tc.ip, got, tc.public)
			}
		})
	}
}

// TestGuardedDialerRefusesLoopback proves the guard is in the dialer rather
// than in a hostname precheck: the URL names a public-looking host, but the
// address it resolves to is loopback, and the connection must still fail.
func TestGuardedDialerRefusesLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"client_id":"x"}`))
	}))
	defer server.Close()

	client := newGuardedHTTPClient()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	response, err := client.Do(request)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected a loopback address to be refused by the dialer guard")
	}
}

func TestResolveRejectsUnknownNonURLClientID(t *testing.T) {
	registry, _ := newTestRegistry(t)

	if _, err := registry.Resolve(context.Background(), "not-registered"); err == nil {
		t.Fatal("an unknown, non-URL client_id must be rejected rather than fetched")
	}
}

func TestResolvePrefersPreRegisteredClient(t *testing.T) {
	registry, store := newTestRegistry(t)
	if err := store.RegisterClient(&Client{
		ClientID:     "https://example.test/client",
		RedirectURIs: []string{"https://example.test/cb"},
		ClientName:   "pre-registered",
	}); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}

	// No HTTP client is wired up, so reaching the network would fail. A
	// pre-registered client must short-circuit before any fetch.
	registry.client = &http.Client{Transport: &rewriteHostTransport{target: "127.0.0.1:1"}}
	client, err := registry.Resolve(context.Background(), "https://example.test/client")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if client.ClientName != "pre-registered" {
		t.Errorf("resolved %q, want the pre-registered client", client.ClientName)
	}
}

func TestFetchClientMetadataValidation(t *testing.T) {
	tests := []struct {
		name     string
		document any
		body     string
		wantErr  string
	}{
		{
			name:    "client_id must equal its URL",
			body:    `{"client_id":"https://elsewhere.test/other","redirect_uris":["https://example.test/cb"]}`,
			wantErr: "does not equal its URL",
		},
		{
			name:    "redirect_uris is required",
			body:    `{"client_id":"%s"}`,
			wantErr: "redirect_uris is required",
		},
		{
			name:    "redirect_uris must be https or loopback",
			body:    `{"client_id":"%s","redirect_uris":["http://evil.test/cb"]}`,
			wantErr: "must be https or a loopback http URL",
		},
		{
			name:    "body must be JSON",
			body:    `not json at all`,
			wantErr: "parse",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var clientID string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				body := tc.body
				if strings.Contains(body, "%s") {
					body = fmt.Sprintf(body, clientID)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			clientID = "https://example.test/client"
			registry, _ := newTestRegistry(t)
			registry.client = &http.Client{
				Transport: &rewriteHostTransport{target: strings.TrimPrefix(server.URL, "http://")},
				Timeout:   cimdFetchTimeout,
			}

			_, err := registry.fetchClientMetadata(context.Background(), clientID)
			if err == nil {
				t.Fatalf("expected an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestFetchClientMetadataAcceptsAValidDocument(t *testing.T) {
	clientID := "https://example.test/client"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(clientMetadataDocument{
			ClientID:     clientID,
			ClientName:   "Example",
			RedirectURIs: []string{"https://example.test/cb", "http://127.0.0.1:9000/cb"},
		})
	}))
	defer server.Close()

	registry, _ := newTestRegistry(t)
	registry.client = &http.Client{
		Transport: &rewriteHostTransport{target: strings.TrimPrefix(server.URL, "http://")},
		Timeout:   cimdFetchTimeout,
	}

	client, err := registry.fetchClientMetadata(context.Background(), clientID)
	if err != nil {
		t.Fatalf("fetchClientMetadata: %v", err)
	}
	if client.ClientID != clientID {
		t.Errorf("client_id = %q, want %q", client.ClientID, clientID)
	}
	// A CIMD client is always public: the document carries no secret.
	if client.IsConfidential() {
		t.Error("a CIMD-registered client must be public")
	}
	if client.TokenEndpointAuthMethod != "none" {
		t.Errorf("token_endpoint_auth_method = %q, want none", client.TokenEndpointAuthMethod)
	}
}

func TestFetchClientMetadataRejectsOversizeBody(t *testing.T) {
	clientID := "https://example.test/client"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"client_id":"` + clientID + `","client_name":"`))
		padding := strings.Repeat("a", cimdMaxBodyBytes+1024)
		_, _ = w.Write([]byte(padding + `","redirect_uris":["https://example.test/cb"]}`))
	}))
	defer server.Close()

	registry, _ := newTestRegistry(t)
	registry.client = &http.Client{
		Transport: &rewriteHostTransport{target: strings.TrimPrefix(server.URL, "http://")},
		Timeout:   cimdFetchTimeout,
	}

	_, err := registry.fetchClientMetadata(context.Background(), clientID)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected an oversize-body rejection, got %v", err)
	}
}

func TestValidateRedirectURI(t *testing.T) {
	tests := []struct {
		uri string
		ok  bool
	}{
		{"https://claude.ai/api/mcp/auth_callback", true},
		{"https://example.test/cb", true},
		{"http://127.0.0.1:9000/cb", true},
		{"http://localhost:9000/cb", true},
		{"http://[::1]:9000/cb", true},
		{"http://example.test/cb", false}, // plain http, not loopback
		{"ftp://example.test/cb", false},
		{"/relative/cb", false},
		{"https://example.test/cb#frag", false},
	}

	for _, tc := range tests {
		t.Run(tc.uri, func(t *testing.T) {
			err := validateRedirectURI(tc.uri)
			if tc.ok && err != nil {
				t.Errorf("validateRedirectURI(%q) = %v, want accepted", tc.uri, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("validateRedirectURI(%q) accepted, want rejected", tc.uri)
			}
		})
	}
}

func TestHasRedirectURIIsExact(t *testing.T) {
	client := &Client{RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}}

	if !HasRedirectURI(client, "https://claude.ai/api/mcp/auth_callback") {
		t.Error("the registered URI must match")
	}
	// Anything looser than exact matching is an open redirect.
	for _, near := range []string{
		"https://claude.ai/api/mcp/auth_callback/",
		"https://claude.ai/api/mcp/auth_callback?x=1",
		"https://claude.ai/api/mcp/auth_callback.evil.test",
		"https://CLAUDE.ai/api/mcp/auth_callback",
	} {
		if HasRedirectURI(client, near) {
			t.Errorf("%q must not match the registered URI", near)
		}
	}
}

func TestHasRedirectURILoopbackPort(t *testing.T) {
	tests := []struct {
		name       string
		registered string
		requested  string
		want       bool
	}{
		{"localhost portless registered", "http://localhost/callback", "http://localhost:53124/callback", true},
		{"IPv4 portless registered", "http://127.0.0.1/callback", "http://127.0.0.1:53124/callback", true},
		{"ported registered", "http://localhost:53124/callback", "http://localhost/callback", true},
		{"different path", "http://localhost:53124/callback", "http://localhost:53124/other", false},
		{"different port and path", "http://localhost:1111/callback", "http://localhost:2222/other", false},
		{"different loopback host", "http://localhost/callback", "http://127.0.0.1:53124/callback", false},
		{"HTTPS request", "http://localhost/callback", "https://localhost:53124/callback", false},
		{"non-loopback port", "https://example.test/callback", "https://example.test:53124/callback", false},
		{"exact match", "http://localhost:53124/callback", "http://localhost:53124/callback", true},
		{"different query", "http://localhost/callback?state=one", "http://localhost:53124/callback?state=two", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{RedirectURIs: []string{test.registered}}
			if got := HasRedirectURI(client, test.requested); got != test.want {
				t.Errorf("HasRedirectURI(%q, %q) = %t, want %t", test.registered, test.requested, got, test.want)
			}
		})
	}
}

func TestRegisterHandler(t *testing.T) {
	registry, store := newTestRegistry(t)
	handler := registry.RegisterHandler()

	t.Run("public client gets no secret", func(t *testing.T) {
		body := `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Desktop"}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", strings.NewReader(body))
		req.RemoteAddr = "203.0.113.1:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
		}
		var resp ClientRegistrationResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ClientSecret != "" {
			t.Error("a client registered with method none must not receive a secret")
		}
		if resp.TokenEndpointAuthMethod != "none" {
			t.Errorf("token_endpoint_auth_method = %q, want none", resp.TokenEndpointAuthMethod)
		}
		if _, ok := store.GetClient(resp.ClientID); !ok {
			t.Error("the registered client should be in the store")
		}
	})

	t.Run("confidential client gets a secret", func(t *testing.T) {
		body := `{"redirect_uris":["https://example.test/cb"],"token_endpoint_auth_method":"client_secret_basic"}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", strings.NewReader(body))
		req.RemoteAddr = "203.0.113.2:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)

		var resp ClientRegistrationResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ClientSecret == "" {
			t.Error("a confidential client must receive a secret")
		}
	})

	t.Run("rejects a bad redirect_uri", func(t *testing.T) {
		body := `{"redirect_uris":["http://evil.test/cb"]}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", strings.NewReader(body))
		req.RemoteAddr = "203.0.113.3:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("rejects a missing redirect_uris", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", strings.NewReader(`{}`))
		req.RemoteAddr = "203.0.113.4:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("rejects GET", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/register", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rec.Code)
		}
	})
}

func TestRegisterHandlerIsRateLimited(t *testing.T) {
	registry, _ := newTestRegistry(t)
	handler := registry.RegisterHandler()
	body := `{"redirect_uris":["https://example.test/cb"]}`

	limited := false
	for i := 0; i < rateLimitCapacity+2; i++ {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", strings.NewReader(body))
		req.RemoteAddr = "198.51.100.7:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("registration must be rate limited per IP")
	}
}

func TestRegisterHandlerHonoursTheClientCap(t *testing.T) {
	registry, store := newTestRegistry(t)
	for i := 0; i < maxClients; i++ {
		if err := store.RegisterClient(&Client{ClientID: fmt.Sprintf("c-%03d", i), CreatedAt: time.Now()}); err != nil {
			t.Fatalf("seeding client %d: %v", i, err)
		}
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", strings.NewReader(`{"redirect_uris":["https://example.test/cb"]}`))
	req.RemoteAddr = "198.51.100.8:1234"
	rec := httptest.NewRecorder()
	registry.RegisterHandler()(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 once the client cap is reached", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{"loopback with no proxy header", "127.0.0.1:5000", "", "127.0.0.1"},
		{"proxied single hop", "127.0.0.1:5000", "203.0.113.9", "203.0.113.9"},
		{"proxied chain takes the first", "127.0.0.1:5000", "203.0.113.9, 10.0.0.1", "203.0.113.9"},
		{"IPv6 loopback trusts the proxy header", "[::1]:5000", "203.0.113.9", "203.0.113.9"},
		{"a direct caller cannot spoof the header", "203.0.113.9:5000", "198.51.100.100", "203.0.113.9"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			if got := clientIP(req); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
