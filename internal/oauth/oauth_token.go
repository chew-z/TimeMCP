package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
)

// TokenResponse is the RFC 6749 section 5.1 successful token response.
type TokenResponse struct {
	// AccessToken is the JWT bearer token.
	AccessToken string `json:"access_token"`
	// TokenType is always "Bearer".
	TokenType string `json:"token_type"`
	// ExpiresIn is the access token's remaining lifetime in seconds.
	ExpiresIn int `json:"expires_in"`
	// RefreshToken is the rotated refresh token.
	RefreshToken string `json:"refresh_token,omitempty"`
	// Scope is the granted scope.
	Scope string `json:"scope"`
}

// TokenService exchanges authorization codes and refresh tokens.
type TokenService struct {
	cfg   Config
	store *Store
	log   *slog.Logger
}

// NewTokenService wires the token endpoint.
func NewTokenService(cfg Config, store *Store, log *slog.Logger) *TokenService {
	if log == nil {
		log = slog.Default()
	}
	return &TokenService{cfg: cfg, store: store, log: log}
}

// TokenHandler serves POST /token.
func (s *TokenService) TokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "the token endpoint requires POST")
			return
		}
		if !s.store.AllowRequest("token", clientIP(r)) {
			writeOAuthError(w, http.StatusTooManyRequests, "invalid_request", "too many token requests")
			return
		}
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
			return
		}

		client, ok := s.authenticateClient(w, r)
		if !ok {
			return
		}

		switch r.PostFormValue("grant_type") {
		case "authorization_code":
			s.exchangeCode(w, r, client)
		case "refresh_token":
			s.exchangeRefresh(w, r, client)
		default:
			writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
				"supported grants are authorization_code and refresh_token")
		}
	}
}

// authenticateClient identifies the caller and enforces the authentication
// method bound at registration.
//
// The registered method is binding, not negotiable per request: a public
// client presenting a secret, or a confidential one omitting it, is rejected
// rather than silently accepted under the other rule.
//
// The public and confidential paths must both be read in one place to see that
// neither can fall through into the other.
//
//nolint:gocyclo // shared with Pushover/Alerts/Answer internal/oauth; keep in sync
func (s *TokenService) authenticateClient(w http.ResponseWriter, r *http.Request) (*Client, bool) {
	basicID, basicSecret, hasBasic := r.BasicAuth()
	postID := r.PostFormValue("client_id")
	postSecret := r.PostFormValue("client_secret")

	clientID := postID
	if hasBasic {
		clientID = basicID
	}
	if clientID == "" {
		s.denyClient(w, "client authentication is required")
		return nil, false
	}

	client, ok := s.store.GetClient(clientID)
	if !ok {
		s.denyClient(w, "unknown client")
		return nil, false
	}

	presented := postSecret
	presentedMethod := "client_secret_post"
	if hasBasic {
		presented = basicSecret
		presentedMethod = "client_secret_basic"
	}
	if presented == "" {
		presentedMethod = "none"
	}

	if !client.IsConfidential() {
		if presented != "" {
			s.denyClient(w, "this client is registered as public and must not present a secret")
			return nil, false
		}
		return client, true
	}

	if presented == "" {
		s.denyClient(w, "this client is registered as confidential and must authenticate")
		return nil, false
	}
	if presentedMethod != client.TokenEndpointAuthMethod {
		s.denyClient(w, "client authentication method does not match the registered one")
		return nil, false
	}
	if subtle.ConstantTimeCompare([]byte(client.ClientSecret), []byte(presented)) != 1 {
		s.denyClient(w, "client authentication failed")
		return nil, false
	}
	return client, true
}

// denyClient emits a 401 invalid_client with the Basic challenge.
func (s *TokenService) denyClient(w http.ResponseWriter, description string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+s.cfg.Realm+`"`)
	writeOAuthError(w, http.StatusUnauthorized, "invalid_client", description)
}

// exchangeCode redeems an authorization code for tokens.
func (s *TokenService) exchangeCode(w http.ResponseWriter, r *http.Request, client *Client) {
	// Codes are single use: TakeCode removes it, so a replay finds nothing
	// even if every other check would have passed.
	code, ok := s.store.TakeCode(r.PostFormValue("code"))
	if !ok {
		s.denyGrant(w, "the authorization code is unknown, expired, or already used")
		return
	}
	if code.ClientID != client.ClientID {
		s.denyGrant(w, "the authorization code was issued to a different client")
		return
	}
	if code.RedirectURI != r.PostFormValue("redirect_uri") {
		s.denyGrant(w, "redirect_uri does not match the authorization request")
		return
	}
	if !verifyPKCE(code.CodeChallenge, r.PostFormValue("code_verifier")) {
		s.denyGrant(w, "the code_verifier does not match the code_challenge")
		return
	}
	if resource := r.PostFormValue("resource"); resource != "" && resource != s.cfg.Resource {
		s.denyGrant(w, "resource does not name this server")
		return
	}

	family, err := newOpaqueToken()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}
	s.issueTokens(w, client, family, code.Scope)
}

// exchangeRefresh rotates a refresh token.
func (s *TokenService) exchangeRefresh(w http.ResponseWriter, r *http.Request, client *Client) {
	presented := r.PostFormValue("refresh_token")
	// Consumption is atomic: the store marks the token rotated under the same
	// lock that reads it, so of two concurrent presentations exactly one sees
	// Rotated false. A separate get-check-mark sequence let both pass.
	stored, ok := s.store.ConsumeRefreshToken(presented)
	if !ok {
		s.denyGrant(w, "the refresh token is unknown or expired")
		return
	}
	if stored.ClientID != client.ClientID {
		s.denyGrant(w, "the refresh token was issued to a different client")
		return
	}

	// RFC 9700 section 4.14.2: a rotated token presented a second time means
	// it leaked. Rejecting only this call would leave the thief's copy of the
	// successor working, so the whole family goes.
	if stored.Rotated {
		s.log.Warn("token: refresh token reuse detected; revoking the family", "client_id", client.ClientID)
		s.store.RevokeFamily(stored.FamilyID)
		s.denyGrant(w, "the refresh token has already been used")
		return
	}

	s.issueTokens(w, client, stored.FamilyID, stored.Scope)
}

// issueTokens mints an access token and a successor refresh token.
func (s *TokenService) issueTokens(w http.ResponseWriter, client *Client, familyID, scope string) {
	if scope == "" {
		scope = s.cfg.Scope
	}
	now := s.cfg.now()

	refreshValue, err := newOpaqueToken()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue a refresh token")
		return
	}

	accessValue, err := s.cfg.Signer.Sign(s.cfg.LoginUser, scope, s.cfg.AccessTokenTTL)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue an access token")
		return
	}
	if err := s.store.PutRefreshToken(&RefreshToken{
		Token:     refreshValue,
		FamilyID:  familyID,
		ClientID:  client.ClientID,
		Audience:  s.cfg.Resource,
		Scope:     scope,
		ExpiresAt: now.Add(s.cfg.RefreshTokenTTL),
	}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not store the refresh token")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	encodeJSON(w, TokenResponse{
		AccessToken:  accessValue,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.cfg.AccessTokenTTL.Seconds()),
		RefreshToken: refreshValue,
		Scope:        scope,
	})
}

// denyGrant rejects a grant.
//
// Every grant failure returns the same error code, because distinguishing an
// unknown code from a bad verifier in the response is an oracle. The specific
// reason is in error_description for a human reading a log, not for a client
// to branch on.
func (s *TokenService) denyGrant(w http.ResponseWriter, description string) {
	writeOAuthError(w, http.StatusBadRequest, "invalid_grant", description)
}

// verifyPKCE checks a code verifier against an S256 challenge.
func verifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(challenge), []byte(computed)) == 1
}
