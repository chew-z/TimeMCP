package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Store ceilings. /register and CIMD-triggering /authorize are reachable
// before any authentication, so an unbounded map behind either one is a
// memory-exhaustion primitive. Every store has a ceiling; none grows with
// traffic.
const (
	// maxClients bounds registered OAuth clients, written by /register.
	maxClients = 100
	// maxPendingAuthorizations bounds authorization requests awaiting login.
	maxPendingAuthorizations = 50
	// maxAuthorizationCodes bounds issued, unredeemed authorization codes.
	maxAuthorizationCodes = 50
	// maxRefreshTokens bounds live refresh tokens across all families.
	maxRefreshTokens = 200
	// maxLoginAttempts is how many password failures one pending
	// authorization tolerates before it is discarded entirely.
	maxLoginAttempts = 5
	// maxRateLimitBuckets bounds the rate limiter's own map, so the defense
	// against abuse cannot itself be abused.
	maxRateLimitBuckets = 1000
)

// Rate limiter shape: a burst of ten, refilling one token every six seconds.
const (
	rateLimitCapacity = 10
	rateLimitRefill   = 6 * time.Second
)

// Client is a registered OAuth client.
type Client struct {
	// ClientID identifies the client. For a CIMD client it is the https URL
	// the metadata document was fetched from.
	ClientID string
	// ClientSecret is empty for a public client.
	ClientSecret string
	// RedirectURIs is the exact set of URIs this client may be redirected to.
	RedirectURIs []string
	// ClientName is the human-readable name shown on the login page.
	ClientName string
	// TokenEndpointAuthMethod is bound at registration and is not negotiable
	// per request: "none", "client_secret_basic" or "client_secret_post".
	TokenEndpointAuthMethod string
	// CreatedAt is when the client was registered.
	CreatedAt time.Time
}

// IsConfidential reports whether the client authenticates at the token
// endpoint. A client registered without a secret is public and relies on PKCE.
func (c *Client) IsConfidential() bool {
	return c != nil && c.ClientSecret != ""
}

// PendingAuthorization is a validated authorization request awaiting login.
type PendingAuthorization struct {
	// ID is the opaque handle carried through the login form.
	ID string
	// ClientID is the client the eventual code is bound to.
	ClientID string
	// RedirectURI is the exact URI validated against the client's set.
	RedirectURI string
	// CodeChallenge is the S256 challenge the token exchange must satisfy.
	CodeChallenge string
	// State is round-tripped to the client byte-for-byte.
	State string
	// Resource is the RFC 8707 resource indicator, if the client sent one.
	Resource string
	// Scope is the granted scope.
	Scope string
	// CSRFToken is bound to this request and compared on login.
	CSRFToken string
	// FailedLogins counts password failures against maxLoginAttempts.
	FailedLogins int
	// CreatedAt orders eviction when the store is full.
	CreatedAt time.Time
	// ExpiresAt is when this request stops being redeemable.
	ExpiresAt time.Time
}

// AuthorizationCode is a single-use code bound to its authorization request.
type AuthorizationCode struct {
	// Code is the opaque value handed to the client.
	Code string
	// ClientID is the client permitted to redeem it.
	ClientID string
	// RedirectURI must match the one presented at the token endpoint.
	RedirectURI string
	// CodeChallenge is verified against the code_verifier on exchange.
	CodeChallenge string
	// Resource is the RFC 8707 resource indicator, if one was requested.
	Resource string
	// Scope is the granted scope.
	Scope string
	// ExpiresAt is the code's short lifetime, typically 60 seconds.
	ExpiresAt time.Time
}

// RefreshToken is an opaque rotating refresh token.
type RefreshToken struct {
	// Token is the opaque value presented at the token endpoint.
	Token string
	// FamilyID groups a token with its predecessors and successors, so that
	// detected reuse can revoke all of them at once.
	FamilyID string
	// ClientID is the client the token was issued to.
	ClientID string
	// Audience is the canonical resource URI carried to the next access token.
	Audience string
	// Scope is the granted scope.
	Scope string
	// Rotated is true once the token has been exchanged. A second use means
	// the token leaked, and the whole family is revoked.
	Rotated bool
	// ExpiresAt is when the token stops being accepted.
	ExpiresAt time.Time
}

// rateLimitBucket is one token bucket in the rate limiter.
type rateLimitBucket struct {
	tokens   float64
	lastSeen time.Time
}

// Store holds every piece of OAuth state behind a single mutex.
//
// One mutex rather than one per map: four maps with four mutexes is four lock
// orders to get right, and nothing here is hot enough to need the
// parallelism. Expiry is lazy — a getter treats an entry at or past its
// deadline as absent and removes it — so there is no sweeper goroutine to
// stop on shutdown.
type Store struct {
	mu sync.Mutex
	// now is injectable so tests can advance the clock instead of sleeping.
	now func() time.Time

	clients  map[string]*Client
	pending  map[string]*PendingAuthorization
	codes    map[string]*AuthorizationCode
	refresh  map[string]*RefreshToken
	limiters map[string]*rateLimitBucket
}

// NewStore returns an empty store. A nil now uses time.Now.
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{
		now:      now,
		clients:  make(map[string]*Client),
		pending:  make(map[string]*PendingAuthorization),
		codes:    make(map[string]*AuthorizationCode),
		refresh:  make(map[string]*RefreshToken),
		limiters: make(map[string]*rateLimitBucket),
	}
}

// RegisterClient stores a client, refusing to grow past maxClients. Replacing
// an existing client_id is allowed and does not consume cap headroom.
func (s *Store) RegisterClient(c *Client) error {
	if c == nil || c.ClientID == "" {
		return fmt.Errorf("register client: client_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.clients[c.ClientID]; !exists && len(s.clients) >= maxClients {
		return fmt.Errorf("register client: registration limit of %d reached", maxClients)
	}
	stored := *c
	s.clients[c.ClientID] = &stored
	return nil
}

// GetClient returns a registered client by its identifier.
func (s *Store) GetClient(clientID string) (*Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clients[clientID]
	if !ok {
		return nil, false
	}
	copied := *c
	return &copied, true
}

// PutPending stores a pending authorization, evicting the oldest when full.
func (s *Store) PutPending(p *PendingAuthorization) error {
	if p == nil || p.ID == "" {
		return fmt.Errorf("put pending authorization: id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.expirePendingLocked()
	if _, exists := s.pending[p.ID]; !exists {
		s.evictOldestLocked(len(s.pending), maxPendingAuthorizations, func() (string, bool) {
			return oldestKey(s.pending, func(v *PendingAuthorization) time.Time { return v.CreatedAt })
		}, func(key string) { delete(s.pending, key) })
	}
	stored := *p
	s.pending[p.ID] = &stored
	return nil
}

// GetPending returns a pending authorization without consuming it, so a failed
// login can re-render the form against the same request.
func (s *Store) GetPending(id string) (*PendingAuthorization, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[id]
	if !ok {
		return nil, false
	}
	if !s.now().Before(p.ExpiresAt) {
		delete(s.pending, id)
		return nil, false
	}
	copied := *p
	return &copied, true
}

// TakePending returns a pending authorization and removes it, so one request
// can be completed exactly once.
func (s *Store) TakePending(id string) (*PendingAuthorization, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[id]
	if !ok {
		return nil, false
	}
	delete(s.pending, id)
	if !s.now().Before(p.ExpiresAt) {
		return nil, false
	}
	copied := *p
	return &copied, true
}

// DeletePending discards a pending authorization.
func (s *Store) DeletePending(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

// RecordLoginFailure counts a failed password attempt and reports how many
// remain. At zero the pending authorization is discarded, so a guessing
// attacker must restart the flow rather than keep hammering one form.
func (s *Store) RecordLoginFailure(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[id]
	if !ok {
		return 0
	}
	p.FailedLogins++
	remaining := maxLoginAttempts - p.FailedLogins
	if remaining <= 0 {
		delete(s.pending, id)
		return 0
	}
	return remaining
}

// PutCode stores an authorization code, evicting the oldest when full.
func (s *Store) PutCode(c *AuthorizationCode) error {
	if c == nil || c.Code == "" {
		return fmt.Errorf("put authorization code: code is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.expireCodesLocked()
	if _, exists := s.codes[c.Code]; !exists {
		s.evictOldestLocked(len(s.codes), maxAuthorizationCodes, func() (string, bool) {
			return oldestKey(s.codes, func(v *AuthorizationCode) time.Time { return v.ExpiresAt })
		}, func(key string) { delete(s.codes, key) })
	}
	stored := *c
	s.codes[c.Code] = &stored
	return nil
}

// TakeCode returns an authorization code and removes it. Codes are single use:
// a second presentation finds nothing.
func (s *Store) TakeCode(code string) (*AuthorizationCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.codes[code]
	if !ok {
		return nil, false
	}
	delete(s.codes, code)
	if !s.now().Before(c.ExpiresAt) {
		return nil, false
	}
	copied := *c
	return &copied, true
}

// PutRefreshToken stores a refresh token, evicting the nearest to expiry when
// full.
func (s *Store) PutRefreshToken(t *RefreshToken) error {
	if t == nil || t.Token == "" {
		return fmt.Errorf("put refresh token: token is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.expireRefreshLocked()
	if _, exists := s.refresh[t.Token]; !exists {
		s.evictOldestLocked(len(s.refresh), maxRefreshTokens, func() (string, bool) {
			return oldestKey(s.refresh, func(v *RefreshToken) time.Time { return v.ExpiresAt })
		}, func(key string) { delete(s.refresh, key) })
	}
	stored := *t
	s.refresh[t.Token] = &stored
	return nil
}

// ConsumeRefreshToken returns a refresh token's pre-consumption state and
// atomically marks it rotated, treating an expired one as absent. The caller
// detects replay through the returned copy's Rotated flag: of two concurrent
// presentations of one token, exactly one sees Rotated false and the other
// sees true and must revoke the family. Check-and-mark happens under one lock
// acquisition, so no interleaving can pass the reuse check twice.
func (s *Store) ConsumeRefreshToken(token string) (*RefreshToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.refresh[token]
	if !ok {
		return nil, false
	}
	if !s.now().Before(t.ExpiresAt) {
		delete(s.refresh, token)
		return nil, false
	}
	copied := *t
	t.Rotated = true
	return &copied, true
}

// RevokeFamily deletes every refresh token sharing a family identifier.
//
// This is the RFC 9700 response to replay: a rotated refresh token presented a
// second time means it leaked, and rejecting only that call would leave the
// thief's copy of the successor working.
func (s *Store) RevokeFamily(familyID string) {
	if familyID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for token, t := range s.refresh {
		if t.FamilyID == familyID {
			delete(s.refresh, token)
		}
	}
}

// AllowRequest reports whether a request may proceed under the token bucket
// for the given bucket name and key, which is typically a client IP.
func (s *Store) AllowRequest(bucket, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := bucket + "|" + key
	now := s.now()
	b, ok := s.limiters[name]
	if !ok {
		s.evictOldestLocked(len(s.limiters), maxRateLimitBuckets, func() (string, bool) {
			return oldestKey(s.limiters, func(v *rateLimitBucket) time.Time { return v.lastSeen })
		}, func(k string) { delete(s.limiters, k) })
		b = &rateLimitBucket{tokens: rateLimitCapacity, lastSeen: now}
		s.limiters[name] = b
	}

	if elapsed := now.Sub(b.lastSeen); elapsed > 0 {
		b.tokens += elapsed.Seconds() / rateLimitRefill.Seconds()
		if b.tokens > rateLimitCapacity {
			b.tokens = rateLimitCapacity
		}
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictOldestLocked frees one slot when a store is at its ceiling. The caller
// holds the mutex.
func (s *Store) evictOldestLocked(size, capacity int, oldest func() (string, bool), remove func(string)) {
	for size >= capacity {
		key, ok := oldest()
		if !ok {
			return
		}
		remove(key)
		size--
	}
}

func (s *Store) expirePendingLocked() {
	now := s.now()
	for id, p := range s.pending {
		if !now.Before(p.ExpiresAt) {
			delete(s.pending, id)
		}
	}
}

func (s *Store) expireCodesLocked() {
	now := s.now()
	for code, c := range s.codes {
		if !now.Before(c.ExpiresAt) {
			delete(s.codes, code)
		}
	}
}

func (s *Store) expireRefreshLocked() {
	now := s.now()
	for token, t := range s.refresh {
		if !now.Before(t.ExpiresAt) {
			delete(s.refresh, token)
		}
	}
}

// oldestKey returns the map key whose deadline sorts earliest. Ties break on
// the key itself so eviction is deterministic under test.
func oldestKey[V any](m map[string]V, deadline func(V) time.Time) (string, bool) {
	if len(m) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	best := keys[0]
	bestAt := deadline(m[best])
	for _, k := range keys[1:] {
		if at := deadline(m[k]); at.Before(bestAt) {
			best, bestAt = k, at
		}
	}
	return best, true
}

// newOpaqueToken returns 256 bits of cryptographic randomness as an unpadded
// base64url string.
//
// An error from crypto/rand is returned rather than being swallowed or
// downgraded to a weaker source: a predictable token is worse than a failed
// request.
func newOpaqueToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
