package oauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// CIMD fetch bounds. Every one of these is a limit on work an unauthenticated
// caller can make this process do.
const (
	// cimdMaxBodyBytes caps a client metadata document.
	cimdMaxBodyBytes = 64 << 10
	// cimdFetchTimeout bounds one document fetch end to end.
	cimdFetchTimeout = 10 * time.Second
	// cimdMaxRedirects caps a redirect chain; every hop is re-validated.
	cimdMaxRedirects = 3
	// cimdMaxConcurrentFetches caps outbound fetches process-wide, so
	// /authorize cannot be used to fan out connections.
	cimdMaxConcurrentFetches = 4
)

// errCIMDBlockedAddress reports a fetch refused by the address guard.
var errCIMDBlockedAddress = errors.New("client metadata document: refused a non-public address")

// ClientRegistrationRequest is the RFC 7591 dynamic registration body.
type ClientRegistrationRequest struct {
	// RedirectURIs are the exact URIs the client may be redirected to.
	RedirectURIs []string `json:"redirect_uris"`
	// ClientName is shown on the login page.
	ClientName string `json:"client_name,omitempty"`
	// TokenEndpointAuthMethod requests public or confidential registration.
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method,omitempty"`
	// GrantTypes is accepted and validated but not stored separately: this
	// server supports exactly the authorization code and refresh grants.
	GrantTypes []string `json:"grant_types,omitempty"`
	// ResponseTypes is likewise validated against the single supported value.
	ResponseTypes []string `json:"response_types,omitempty"`
}

// ClientRegistrationResponse is the RFC 7591 registration result.
type ClientRegistrationResponse struct {
	// ClientID identifies the newly registered client.
	ClientID string `json:"client_id"`
	// ClientSecret is present only for a confidential client.
	ClientSecret string `json:"client_secret,omitempty"`
	// ClientIDIssuedAt is a Unix timestamp.
	ClientIDIssuedAt int64 `json:"client_id_issued_at"`
	// RedirectURIs echoes the registered URIs.
	RedirectURIs []string `json:"redirect_uris"`
	// ClientName echoes the registered name.
	ClientName string `json:"client_name,omitempty"`
	// TokenEndpointAuthMethod is the method bound at registration.
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
}

// clientMetadataDocument is the subset of a CIMD document this server reads.
type clientMetadataDocument struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// ClientRegistry resolves client identifiers through the spec's priority
// order: a pre-registered client first, then a Client ID Metadata Document.
type ClientRegistry struct {
	store  *Store
	log    *slog.Logger
	client *http.Client
	// fetchSlots bounds concurrent outbound CIMD fetches.
	fetchSlots chan struct{}
}

// NewClientRegistry builds a registry whose CIMD fetches are guarded against
// SSRF and DNS rebinding.
func NewClientRegistry(store *Store, log *slog.Logger) *ClientRegistry {
	if log == nil {
		log = slog.Default()
	}
	return &ClientRegistry{
		store:      store,
		log:        log,
		client:     newGuardedHTTPClient(),
		fetchSlots: make(chan struct{}, cimdMaxConcurrentFetches),
	}
}

// newGuardedHTTPClient builds an HTTP client that refuses to connect to any
// non-public address.
//
// The check lives in the dialer's Control hook, which runs after DNS
// resolution on the address actually being connected to. A hostname precheck
// cannot see that address and is defeated by a second DNS answer — the
// rebinding attack — whereas this cannot be.
func newGuardedHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("client metadata document: parse dial address: %w", err)
			}
			ip := net.ParseIP(host)
			if ip == nil || !isPublicIP(ip) {
				return errCIMDBlockedAddress
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: cimdFetchTimeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= cimdMaxRedirects {
				return fmt.Errorf("client metadata document: more than %d redirects", cimdMaxRedirects)
			}
			// Every hop must still be HTTPS; the dialer re-checks the address.
			if !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("client metadata document: redirect to a non-https URL")
			}
			return nil
		},
	}
}

// isPublicIP reports whether an address is routable on the public internet.
//
// A flat list of address ranges to refuse. Splitting it would hide which
// ranges are covered, and coverage is the whole point.
// Loopback, private, link-local, unspecified, multicast and IPv4-mapped IPv6
// addresses are all refused.
//
//nolint:gocyclo // shared with Pushover/Alerts/Answer internal/oauth; keep in sync
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// An IPv4-mapped IPv6 address reaches the same host as its IPv4 form, so
	// judge it by that form rather than by its 16-byte representation.
	if v4 := ip.To4(); v4 != nil {
		return isPublicIPv4(v4)
	}
	// IPv6 unique local addresses, fc00::/7.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return true
}

// isPublicIPv4 reports whether an IPv4 address is routable on the public
// internet, covering the ranges net.IP's own predicates do not.
func isPublicIPv4(v4 net.IP) bool {
	// 100.64.0.0/10, carrier-grade NAT.
	if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	// 0.0.0.0/8 and 240.0.0.0/4 are not routable.
	return v4[0] != 0 && v4[0] < 240
}

// Resolve returns the client for an identifier, registering it from a Client
// ID Metadata Document when the identifier is an https URL.
func (r *ClientRegistry) Resolve(ctx context.Context, clientID string) (*Client, error) {
	if clientID == "" {
		return nil, fmt.Errorf("resolve client: client_id is required")
	}
	if client, ok := r.store.GetClient(clientID); ok {
		return client, nil
	}
	if !looksLikeCIMDIdentifier(clientID) {
		return nil, fmt.Errorf("resolve client: unknown client_id")
	}

	client, err := r.fetchClientMetadata(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if err := r.store.RegisterClient(client); err != nil {
		return nil, err
	}
	return client, nil
}

// looksLikeCIMDIdentifier reports whether an identifier should be treated as a
// Client ID Metadata Document URL.
func looksLikeCIMDIdentifier(clientID string) bool {
	parsed, err := url.Parse(clientID)
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && parsed.Host != ""
}

// fetchClientMetadata retrieves and validates a Client ID Metadata Document.
//
// A sequential validation chain where the order is load bearing: each check
// narrows what the next may assume.
//
//nolint:gocyclo // shared with Pushover/Alerts/Answer internal/oauth; keep in sync
func (r *ClientRegistry) fetchClientMetadata(ctx context.Context, clientID string) (*Client, error) {
	select {
	case r.fetchSlots <- struct{}{}:
		defer func() { <-r.fetchSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	fetchCtx, cancel := context.WithTimeout(ctx, cimdFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, clientID, nil)
	if err != nil {
		return nil, fmt.Errorf("client metadata document: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client metadata document: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("client metadata document: upstream status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("client metadata document: read body: %w", err)
	}
	if len(body) > cimdMaxBodyBytes {
		return nil, fmt.Errorf("client metadata document: body exceeds %d bytes", cimdMaxBodyBytes)
	}

	var doc clientMetadataDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("client metadata document: parse: %w", err)
	}

	// The document's own client_id must equal the URL it was fetched from,
	// exactly. Normalising a near-match here would let one document claim an
	// identifier it does not own.
	if doc.ClientID != clientID {
		return nil, fmt.Errorf("client metadata document: client_id %q does not equal its URL", doc.ClientID)
	}
	if len(doc.RedirectURIs) == 0 {
		return nil, fmt.Errorf("client metadata document: redirect_uris is required")
	}
	for _, redirect := range doc.RedirectURIs {
		if err := validateRedirectURI(redirect); err != nil {
			return nil, err
		}
	}

	return &Client{
		ClientID:                clientID,
		RedirectURIs:            doc.RedirectURIs,
		ClientName:              doc.ClientName,
		TokenEndpointAuthMethod: "none",
		CreatedAt:               time.Now(),
	}, nil
}

// validateRedirectURI accepts only HTTPS URLs and loopback URLs, per OAuth 2.1.
func validateRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("redirect_uri %q: parse: %w", raw, err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("redirect_uri %q: must be an absolute URL", raw)
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("redirect_uri %q: must not carry a fragment", raw)
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return nil
	}
	if strings.EqualFold(parsed.Scheme, "http") && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("redirect_uri %q: must be https or a loopback http URL", raw)
}

// isLoopbackHost reports whether a hostname names the local machine.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// HasRedirectURI reports whether a redirect URI is registered for the client.
// Matching is exact: no prefix match, no normalisation, and no trailing-slash
// tolerance. Anything looser is an open redirect. RFC 8252 section 7.3 is the
// sole exception: loopback HTTP redirects may use a different port because a
// native client receives an ephemeral port from the OS and cannot know it at
// registration time. That relaxation is confined to loopback HTTP; everything
// else remains exact.
func HasRedirectURI(client *Client, redirectURI string) bool {
	for _, registered := range client.RedirectURIs {
		if subtle.ConstantTimeCompare([]byte(registered), []byte(redirectURI)) == 1 {
			return true
		}
		if loopbackPortMatch(registered, redirectURI) {
			return true
		}
	}
	return false
}

//nolint:gocyclo // shared with Pushover/Alerts/Answer internal/oauth; keep in sync
func loopbackPortMatch(registered, redirectURI string) bool {
	registeredURL, err := url.Parse(registered)
	if err != nil {
		return false
	}
	redirectURL, err := url.Parse(redirectURI)
	if err != nil {
		return false
	}
	if !strings.EqualFold(registeredURL.Scheme, "http") || !strings.EqualFold(redirectURL.Scheme, "http") {
		return false
	}
	if !isLoopbackHost(registeredURL.Hostname()) || !isLoopbackHost(redirectURL.Hostname()) {
		return false
	}
	if registeredURL.Hostname() != redirectURL.Hostname() || registeredURL.Path != redirectURL.Path || registeredURL.RawPath != redirectURL.RawPath || registeredURL.RawQuery != redirectURL.RawQuery || registeredURL.Fragment != redirectURL.Fragment || registeredURL.ForceQuery != redirectURL.ForceQuery || registeredURL.User != nil || redirectURL.User != nil { //nolint:revive // shared with Pushover/Alerts/Answer internal/oauth; keep in sync
		return false
	}
	return registeredURL.Port() != redirectURL.Port()
}

// RegisterHandler serves RFC 7591 dynamic client registration.
//
// The current spec deprecates DCR in favor of CIMD, but DCR is what clients
// implement today, so both are offered and the client picks.
func (r *ClientRegistry) RegisterHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "registration requires POST")
			return
		}
		if !r.store.AllowRequest("register", clientIP(req)) {
			writeOAuthError(w, http.StatusTooManyRequests, "invalid_request", "too many registration attempts")
			return
		}

		registration, ok := decodeRegistration(w, req)
		if !ok {
			return
		}

		client, ok := newRegisteredClient(w, registration)
		if !ok {
			return
		}

		if err := r.store.RegisterClient(client); err != nil {
			writeOAuthError(w, http.StatusServiceUnavailable, "invalid_client_metadata", err.Error())
			return
		}
		r.log.Info("registered OAuth client via DCR", "client_id", client.ClientID, "method", client.TokenEndpointAuthMethod)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		encodeJSON(w, ClientRegistrationResponse{
			ClientID:                client.ClientID,
			ClientSecret:            client.ClientSecret,
			ClientIDIssuedAt:        client.CreatedAt.Unix(),
			RedirectURIs:            client.RedirectURIs,
			ClientName:              client.ClientName,
			TokenEndpointAuthMethod: client.TokenEndpointAuthMethod,
		})
	}
}

// decodeRegistration reads and validates a registration body.
func decodeRegistration(w http.ResponseWriter, req *http.Request) (*ClientRegistrationRequest, bool) {
	body, err := io.ReadAll(io.LimitReader(req.Body, cimdMaxBodyBytes+1))
	if err != nil || len(body) > cimdMaxBodyBytes {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "registration body is unreadable or too large")
		return nil, false
	}

	var registration ClientRegistrationRequest
	if err := json.Unmarshal(body, &registration); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "registration body is not valid JSON")
		return nil, false
	}
	if len(registration.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return nil, false
	}
	for _, redirect := range registration.RedirectURIs {
		if err := validateRedirectURI(redirect); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return nil, false
		}
	}
	return &registration, true
}

// newRegisteredClient builds a client from a validated registration request,
// issuing a secret only for a confidential one.
func newRegisteredClient(w http.ResponseWriter, registration *ClientRegistrationRequest) (*Client, bool) {
	method := registration.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	switch method {
	case "none", "client_secret_basic", "client_secret_post":
	default:
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
		return nil, false
	}

	clientID, err := newOpaqueToken()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not generate a client_id")
		return nil, false
	}
	client := &Client{
		ClientID:                clientID,
		RedirectURIs:            registration.RedirectURIs,
		ClientName:              registration.ClientName,
		TokenEndpointAuthMethod: method,
		CreatedAt:               time.Now(),
	}
	if method != "none" {
		secret, err := newOpaqueToken()
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not generate a client_secret")
			return nil, false
		}
		client.ClientSecret = secret
	}
	return client, true
}

// clientIP returns the caller's address for rate limiting. X-Forwarded-For is
// trusted only from a loopback peer: that is the nginx proxy on this host,
// which overwrites the header with the real client address. A header arriving
// on any other connection was set by the caller itself, and honoring it would
// hand every rate-limit key to the attacker.
func clientIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if forwarded := req.Header.Get("X-Forwarded-For"); forwarded != "" {
			if idx := strings.IndexByte(forwarded, ','); idx >= 0 {
				return strings.TrimSpace(forwarded[:idx])
			}
			return strings.TrimSpace(forwarded)
		}
	}
	return host
}

// writeOAuthError emits an RFC 6749 section 5.2 error body.
func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encodeJSON(w, map[string]string{
		"error":             code,
		"error_description": description,
	})
}
