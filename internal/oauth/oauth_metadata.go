package oauth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// ProtectedResourceMetadata is the RFC 9728 document describing this server as
// a protected resource.
type ProtectedResourceMetadata struct {
	// Resource is the canonical resource URI, which is also every token's aud.
	Resource string `json:"resource"`
	// AuthorizationServers names the issuers permitted to mint tokens for it.
	AuthorizationServers []string `json:"authorization_servers"`
	// ScopesSupported lists the scopes a client may request.
	ScopesSupported []string `json:"scopes_supported"`
	// BearerMethodsSupported says tokens arrive in the Authorization header.
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// AuthorizationServerMetadata is the RFC 8414 document describing this
// server's authorization endpoints.
type AuthorizationServerMetadata struct {
	// Issuer must equal the identifier used to build the well-known URL.
	Issuer string `json:"issuer"`
	// AuthorizationEndpoint begins the authorization code flow.
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	// TokenEndpoint exchanges codes and refresh tokens.
	TokenEndpoint string `json:"token_endpoint"`
	// RegistrationEndpoint is RFC 7591 dynamic client registration, retained
	// although the current spec deprecates it in favor of CIMD.
	RegistrationEndpoint string `json:"registration_endpoint"`
	// ScopesSupported lists the scopes a client may request.
	ScopesSupported []string `json:"scopes_supported"`
	// ResponseTypesSupported is code only: OAuth 2.1 removes the implicit flow.
	ResponseTypesSupported []string `json:"response_types_supported"`
	// GrantTypesSupported is the authorization code and refresh grants.
	GrantTypesSupported []string `json:"grant_types_supported"`
	// CodeChallengeMethodsSupported is S256 only. A compliant client that
	// finds this field absent refuses to start the flow, so omitting it
	// breaks discovery rather than loosening it.
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	// TokenEndpointAuthMethodsSupported covers public and confidential clients.
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	// AuthorizationResponseIssParameterSupported advertises RFC 9207.
	AuthorizationResponseIssParameterSupported bool `json:"authorization_response_iss_parameter_supported"`
	// ClientIDMetadataDocumentSupported advertises CIMD registration.
	ClientIDMetadataDocumentSupported bool `json:"client_id_metadata_document_supported"`
}

// publicURL builds an externally visible URL from the configured issuer.
//
// nginx can strip a /time prefix before the request reaches this
// process, so a route is registered at "/token" while its advertised URL is
// "https://karma.example/time/token". Everything published in metadata
// goes through here so the two cannot drift.
func publicURL(cfg Config, segment string) string {
	return strings.TrimRight(cfg.Issuer, "/") + segment
}

// protectedResourceMetadata builds the RFC 9728 document.
func protectedResourceMetadata(cfg Config) ProtectedResourceMetadata {
	return ProtectedResourceMetadata{
		Resource:               cfg.Resource,
		AuthorizationServers:   []string{cfg.Issuer},
		ScopesSupported:        []string{cfg.Scope},
		BearerMethodsSupported: []string{"header"},
	}
}

// authorizationServerMetadata builds the RFC 8414 document.
func authorizationServerMetadata(cfg Config) AuthorizationServerMetadata {
	return AuthorizationServerMetadata{
		Issuer:                        cfg.Issuer,
		AuthorizationEndpoint:         publicURL(cfg, "/authorize"),
		TokenEndpoint:                 publicURL(cfg, "/token"),
		RegistrationEndpoint:          publicURL(cfg, "/register"),
		ScopesSupported:               []string{cfg.Scope},
		ResponseTypesSupported:        []string{"code"},
		GrantTypesSupported:           []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported: []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{
			"none", "client_secret_basic", "client_secret_post",
		},
		AuthorizationResponseIssParameterSupported: true,
		ClientIDMetadataDocumentSupported:          true,
	}
}

// ProtectedResourceMetadataPath returns the RFC 9728 probe path for resource.
//
// The path is the resource URI's path component appended to the well-known
// prefix, so a resource served under a sub-path is probed one segment deeper
// than the host root.
func ProtectedResourceMetadataPath(resource string) string {
	path := resourcePath(resource)
	if path == "" {
		return "/.well-known/oauth-protected-resource"
	}
	return "/.well-known/oauth-protected-resource" + path
}

// authorizationServerMetadataPath is the RFC 8414 discovery path.
//
// The issuer has a path component, so the path is inserted between the
// well-known prefix and nothing else. Serving this document at the bare
// /.well-known/oauth-authorization-server instead is a silent discovery
// failure: the client simply never finds it.
func authorizationServerMetadataPath(issuer string) string {
	path := resourcePath(issuer)
	if path == "" {
		return "/.well-known/oauth-authorization-server"
	}
	return "/.well-known/oauth-authorization-server" + path
}

// resourcePath returns the path component of an absolute URL, without a
// trailing slash. It returns "" for a URL that has no path.
func resourcePath(raw string) string {
	rest := raw
	if idx := strings.Index(rest, "://"); idx >= 0 {
		rest = rest[idx+3:]
	}
	idx := strings.Index(rest, "/")
	if idx < 0 {
		return ""
	}
	return strings.TrimRight(rest[idx:], "/")
}

// IssuerOrigin returns issuer's scheme and host without a path.
func IssuerOrigin(issuer string) string {
	parsed, err := url.Parse(issuer)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// handleProtectedResourceMetadata serves the RFC 9728 document.
func handleProtectedResourceMetadata(cfg Config) http.HandlerFunc {
	return metadataHandler(func() any { return protectedResourceMetadata(cfg) })
}

// handleAuthorizationServerMetadata serves the RFC 8414 document.
func handleAuthorizationServerMetadata(cfg Config) http.HandlerFunc {
	return metadataHandler(func() any { return authorizationServerMetadata(cfg) })
}

// metadataHandler serves a discovery document on GET and HEAD.
func metadataHandler(build func() any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Discovery documents are stable for the life of a deployment, and a
		// client re-fetches them on every connection.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		encodeJSON(w, build())
	}
}

// encodeJSON writes a JSON body and logs encode failures because the status
// line is already sent and an HTTP error response can no longer be written.
func encodeJSON(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Default().Warn("oauth: encoding JSON response", "error", err)
	}
}
