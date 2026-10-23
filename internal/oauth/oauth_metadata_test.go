package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func metadataTestConfig() Config {
	return Config{
		Issuer:   "https://karma.example/time",
		Resource: "https://karma.example/time/mcp",
		Scope:    "time",
	}
}

func TestMetadataPaths(t *testing.T) {
	cfg := metadataTestConfig()

	// The RFC 9728 probe path is the resource's path appended to the
	// well-known prefix, which is one segment deeper than the /gemini
	// precedent on the same host.
	if got, want := ProtectedResourceMetadataPath(cfg.Resource), "/.well-known/oauth-protected-resource/time/mcp"; got != want {
		t.Errorf("protected-resource path = %q, want %q", got, want)
	}
	// The issuer has a path, so RFC 8414 requires path insertion.
	if got, want := authorizationServerMetadataPath(cfg.Issuer), "/.well-known/oauth-authorization-server/time"; got != want {
		t.Errorf("authorization-server path = %q, want %q", got, want)
	}
}

func TestMetadataPathsWithoutPathComponent(t *testing.T) {
	cfg := &Config{Issuer: "https://example.test", Resource: "https://example.test"}

	if got, want := ProtectedResourceMetadataPath(cfg.Resource), "/.well-known/oauth-protected-resource"; got != want {
		t.Errorf("protected-resource path = %q, want %q", got, want)
	}
	if got, want := authorizationServerMetadataPath(cfg.Issuer), "/.well-known/oauth-authorization-server"; got != want {
		t.Errorf("authorization-server path = %q, want %q", got, want)
	}
}

// TestIssuerEqualsTheURLItWasBuiltFrom asserts the RFC 8414 invariant that
// costs an afternoon when it is wrong: the issuer in the document must equal
// the identifier the well-known URL was derived from.
func TestIssuerEqualsTheURLItWasBuiltFrom(t *testing.T) {
	cfg := metadataTestConfig()
	path := authorizationServerMetadataPath(cfg.Issuer)

	rec := httptest.NewRecorder()
	handleAuthorizationServerMetadata(cfg)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

	var doc AuthorizationServerMetadata
	if err := json.NewDecoder(rec.Body).Decode(&doc); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	// Reconstruct the issuer from the path the client would have fetched.
	reconstructed := "https://karma.example" + trimWellKnownPrefix(path, "/.well-known/oauth-authorization-server")
	if doc.Issuer != reconstructed {
		t.Errorf("issuer = %q, but the URL it is served at implies %q", doc.Issuer, reconstructed)
	}
}

func trimWellKnownPrefix(path, prefix string) string {
	if len(path) <= len(prefix) {
		return ""
	}
	return path[len(prefix):]
}

func TestAuthorizationServerMetadataContents(t *testing.T) {
	cfg := metadataTestConfig()
	doc := authorizationServerMetadata(cfg)

	// S256 only. Absent breaks compliant clients; containing "plain" defeats
	// the point of PKCE. Neither is visible from a working connection.
	if !reflect.DeepEqual(doc.CodeChallengeMethodsSupported, []string{"S256"}) {
		t.Errorf("code_challenge_methods_supported = %v, want [S256]", doc.CodeChallengeMethodsSupported)
	}
	if !doc.ClientIDMetadataDocumentSupported {
		t.Error("client_id_metadata_document_supported must be true")
	}
	if !doc.AuthorizationResponseIssParameterSupported {
		t.Error("authorization_response_iss_parameter_supported must be true")
	}
	if !reflect.DeepEqual(doc.ResponseTypesSupported, []string{"code"}) {
		t.Errorf("response_types_supported = %v, want [code]", doc.ResponseTypesSupported)
	}

	for name, got := range map[string]string{
		"authorization_endpoint": doc.AuthorizationEndpoint,
		"token_endpoint":         doc.TokenEndpoint,
		"registration_endpoint":  doc.RegistrationEndpoint,
	} {
		if want := cfg.Issuer; len(got) <= len(want) || got[:len(want)] != want {
			t.Errorf("%s = %q, which is not an absolute URL under the issuer %q", name, got, want)
		}
	}
}

func TestProtectedResourceMetadataContents(t *testing.T) {
	cfg := metadataTestConfig()
	doc := protectedResourceMetadata(cfg)

	if doc.Resource != cfg.Resource {
		t.Errorf("resource = %q, want %q", doc.Resource, cfg.Resource)
	}
	if !reflect.DeepEqual(doc.AuthorizationServers, []string{cfg.Issuer}) {
		t.Errorf("authorization_servers = %v, want [%s]", doc.AuthorizationServers, cfg.Issuer)
	}
	if !reflect.DeepEqual(doc.ScopesSupported, []string{cfg.Scope}) {
		t.Errorf("scopes_supported = %v, want [%s]", doc.ScopesSupported, cfg.Scope)
	}
}

func TestMetadataHandlerMethodsAndHeaders(t *testing.T) {
	cfg := metadataTestConfig()
	handler := handleProtectedResourceMetadata(cfg)

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/.well-known/oauth-protected-resource", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}

func TestPublicURL(t *testing.T) {
	cfg := metadataTestConfig()

	if got, want := publicURL(cfg, "/token"), "https://karma.example/time/token"; got != want {
		t.Errorf("publicURL = %q, want %q", got, want)
	}
	// A stray trailing slash on the issuer must not produce a doubled one.
	trailing := Config{Issuer: "https://karma.example/time/"}
	if got, want := publicURL(trailing, "/token"), "https://karma.example/time/token"; got != want {
		t.Errorf("publicURL with a trailing slash = %q, want %q", got, want)
	}
}

func TestMountDiscoveryDocuments(t *testing.T) {
	tests := []struct {
		name, issuer, resource, authorizationPath, protectedPath string
	}{
		{
			name:              "pathless issuer",
			issuer:            "http://127.0.0.1:8080",
			resource:          "http://127.0.0.1:8080/mcp",
			authorizationPath: "/.well-known/oauth-authorization-server",
			protectedPath:     "/.well-known/oauth-protected-resource/mcp",
		},
		{
			name:              "issuer with path",
			issuer:            "http://127.0.0.1:8080/time",
			resource:          "http://127.0.0.1:8080/mcp",
			authorizationPath: "/.well-known/oauth-authorization-server/time",
			protectedPath:     "/.well-known/oauth-protected-resource/mcp",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{Issuer: test.issuer, Resource: test.resource, Scope: "time"}
			mux := http.NewServeMux()
			Mount(mux, cfg, NewStore(nil), nil)

			paths := []string{test.authorizationPath, test.protectedPath}
			if test.name == "pathless issuer" {
				paths = append(paths, "/.well-known/oauth-protected-resource")
			}
			for _, path := range paths {
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("%s response = %d %q", path, response.Code, response.Header().Get("Content-Type"))
				}
			}

			authorizationResponse := httptest.NewRecorder()
			mux.ServeHTTP(authorizationResponse, httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.authorizationPath, nil))
			var authorization AuthorizationServerMetadata
			if err := json.NewDecoder(authorizationResponse.Body).Decode(&authorization); err != nil {
				t.Fatalf("decode authorization metadata: %v", err)
			}
			if authorization.Issuer != cfg.Issuer || !reflect.DeepEqual(authorization.ScopesSupported, []string{cfg.Scope}) ||
				!reflect.DeepEqual(authorization.CodeChallengeMethodsSupported, []string{"S256"}) {
				t.Fatalf("authorization metadata = %+v", authorization)
			}

			protectedResponse := httptest.NewRecorder()
			mux.ServeHTTP(protectedResponse, httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.protectedPath, nil))
			var protected ProtectedResourceMetadata
			if err := json.NewDecoder(protectedResponse.Body).Decode(&protected); err != nil {
				t.Fatalf("decode protected metadata: %v", err)
			}
			if protected.Resource != cfg.Resource || !reflect.DeepEqual(protected.AuthorizationServers, []string{cfg.Issuer}) ||
				!reflect.DeepEqual(protected.ScopesSupported, []string{cfg.Scope}) || !reflect.DeepEqual(protected.BearerMethodsSupported, []string{"header"}) {
				t.Fatalf("protected metadata = %+v", protected)
			}

			post := httptest.NewRecorder()
			mux.ServeHTTP(post, httptest.NewRequestWithContext(t.Context(), http.MethodPost, test.protectedPath, nil))
			if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
				t.Fatalf("POST response = %d Allow=%q", post.Code, post.Header().Get("Allow"))
			}
		})
	}
}
