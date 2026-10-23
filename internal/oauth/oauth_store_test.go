package oauth

import (
	"encoding/base64"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is an injectable clock so store tests advance time instead of
// sleeping.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func newTestStore() (*Store, *fakeClock) {
	clock := newFakeClock()
	return NewStore(clock.Now), clock
}

func TestRegisterClientCapAndReplacement(t *testing.T) {
	store, clock := newTestStore()

	for i := 0; i < maxClients; i++ {
		err := store.RegisterClient(&Client{ClientID: fmt.Sprintf("client-%03d", i), CreatedAt: clock.Now()})
		if err != nil {
			t.Fatalf("registering client %d: %v", i, err)
		}
	}
	if err := store.RegisterClient(&Client{ClientID: "one-too-many"}); err == nil {
		t.Fatal("expected registration past the cap to be refused")
	}

	// Replacing an existing client must not consume cap headroom, or a client
	// re-registering would be locked out of its own identifier.
	if err := store.RegisterClient(&Client{ClientID: "client-000", ClientName: "replaced"}); err != nil {
		t.Fatalf("replacing an existing client: %v", err)
	}
	got, ok := store.GetClient("client-000")
	if !ok || got.ClientName != "replaced" {
		t.Fatalf("expected the replacement to be stored, got %+v (ok=%v)", got, ok)
	}
}

func TestClientConfidentiality(t *testing.T) {
	public := &Client{ClientID: "public"}
	confidential := &Client{ClientID: "confidential", ClientSecret: "s"}

	if public.IsConfidential() {
		t.Error("a client with no secret must be public")
	}
	if !confidential.IsConfidential() {
		t.Error("a client with a secret must be confidential")
	}
}

func TestPendingSingleUseAndPeek(t *testing.T) {
	store, clock := newTestStore()
	pending := &PendingAuthorization{
		ID:        "pending-1",
		ClientID:  "c",
		CreatedAt: clock.Now(),
		ExpiresAt: clock.Now().Add(5 * time.Minute),
	}
	if err := store.PutPending(pending); err != nil {
		t.Fatalf("PutPending: %v", err)
	}

	// GetPending must not consume: a failed login re-renders the same form.
	if _, ok := store.GetPending("pending-1"); !ok {
		t.Fatal("first GetPending should find the entry")
	}
	if _, ok := store.GetPending("pending-1"); !ok {
		t.Fatal("GetPending must not delete the entry")
	}

	if _, ok := store.TakePending("pending-1"); !ok {
		t.Fatal("TakePending should find the entry")
	}
	if _, ok := store.TakePending("pending-1"); ok {
		t.Fatal("TakePending must consume the entry")
	}
}

func TestPendingExpiry(t *testing.T) {
	store, clock := newTestStore()
	if err := store.PutPending(&PendingAuthorization{
		ID:        "p",
		CreatedAt: clock.Now(),
		ExpiresAt: clock.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}

	clock.Advance(time.Minute)
	if _, ok := store.GetPending("p"); ok {
		t.Fatal("an entry at its deadline must be treated as absent")
	}
}

func TestRecordLoginFailureDropsPendingAtLimit(t *testing.T) {
	store, clock := newTestStore()
	if err := store.PutPending(&PendingAuthorization{
		ID:        "p",
		CreatedAt: clock.Now(),
		ExpiresAt: clock.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}

	for attempt := 1; attempt < maxLoginAttempts; attempt++ {
		remaining := store.RecordLoginFailure("p")
		if want := maxLoginAttempts - attempt; remaining != want {
			t.Fatalf("attempt %d: remaining = %d, want %d", attempt, remaining, want)
		}
	}
	if remaining := store.RecordLoginFailure("p"); remaining != 0 {
		t.Fatalf("final attempt: remaining = %d, want 0", remaining)
	}
	if _, ok := store.GetPending("p"); ok {
		t.Fatal("the pending authorization must be dropped once attempts are exhausted")
	}
}

func TestAuthorizationCodeSingleUseAndExpiry(t *testing.T) {
	store, clock := newTestStore()
	if err := store.PutCode(&AuthorizationCode{Code: "abc", ExpiresAt: clock.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("PutCode: %v", err)
	}

	if _, ok := store.TakeCode("abc"); !ok {
		t.Fatal("first TakeCode should succeed")
	}
	if _, ok := store.TakeCode("abc"); ok {
		t.Fatal("a code must be redeemable exactly once")
	}

	if err := store.PutCode(&AuthorizationCode{Code: "stale", ExpiresAt: clock.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("PutCode: %v", err)
	}
	clock.Advance(2 * time.Minute)
	if _, ok := store.TakeCode("stale"); ok {
		t.Fatal("an expired code must not be returned")
	}
}

func TestRefreshRotationAndFamilyRevocation(t *testing.T) {
	store, clock := newTestStore()
	expires := clock.Now().Add(time.Hour)

	for _, tok := range []*RefreshToken{
		{Token: "r1", FamilyID: "fam-a", ExpiresAt: expires},
		{Token: "r2", FamilyID: "fam-a", ExpiresAt: expires},
		{Token: "other", FamilyID: "fam-b", ExpiresAt: expires},
	} {
		if err := store.PutRefreshToken(tok); err != nil {
			t.Fatalf("PutRefreshToken %s: %v", tok.Token, err)
		}
	}

	first, ok := store.ConsumeRefreshToken("r1")
	if !ok || first.Rotated {
		t.Fatalf("the first consumption of r1 should report it unrotated, got %+v (ok=%v)", first, ok)
	}
	second, ok := store.ConsumeRefreshToken("r1")
	if !ok || !second.Rotated {
		t.Fatalf("the second consumption of r1 should report the replay, got %+v (ok=%v)", second, ok)
	}

	store.RevokeFamily("fam-a")
	if _, ok := store.ConsumeRefreshToken("r1"); ok {
		t.Error("revoking a family must delete its rotated token")
	}
	if _, ok := store.ConsumeRefreshToken("r2"); ok {
		t.Error("revoking a family must delete its successor")
	}
	if _, ok := store.ConsumeRefreshToken("other"); !ok {
		t.Error("revoking one family must leave another intact")
	}
}

func TestConcurrentRefreshConsumptionMintsOneSuccessor(t *testing.T) {
	store, clock := newTestStore()
	if err := store.PutRefreshToken(&RefreshToken{
		Token: "r", FamilyID: "fam", ExpiresAt: clock.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("PutRefreshToken: %v", err)
	}

	const presenters = 8
	minted := make(chan bool, presenters)
	var wg sync.WaitGroup
	for i := 0; i < presenters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stored, ok := store.ConsumeRefreshToken("r")
			minted <- ok && !stored.Rotated
		}()
	}
	wg.Wait()
	close(minted)

	successors := 0
	for wouldIssue := range minted {
		if wouldIssue {
			successors++
		}
	}
	if successors != 1 {
		t.Fatalf("%d concurrent presentations would mint a successor; exactly one may", successors)
	}
}

func TestStoreCapsEvictRatherThanGrow(t *testing.T) {
	store, clock := newTestStore()
	now := clock.Now()
	far := now.Add(24 * time.Hour)

	for i := 0; i < maxPendingAuthorizations+10; i++ {
		if err := store.PutPending(&PendingAuthorization{
			ID:        fmt.Sprintf("p-%03d", i),
			CreatedAt: now.Add(time.Duration(i) * time.Second),
			ExpiresAt: far,
		}); err != nil {
			t.Fatalf("PutPending %d: %v", i, err)
		}
	}
	for i := 0; i < maxAuthorizationCodes+10; i++ {
		if err := store.PutCode(&AuthorizationCode{
			Code:      fmt.Sprintf("c-%03d", i),
			ExpiresAt: far.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("PutCode %d: %v", i, err)
		}
	}
	for i := 0; i < maxRefreshTokens+10; i++ {
		if err := store.PutRefreshToken(&RefreshToken{
			Token:     fmt.Sprintf("r-%04d", i),
			ExpiresAt: far.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("PutRefreshToken %d: %v", i, err)
		}
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	for _, tc := range []struct {
		name string
		size int
		cap  int
	}{
		{"pending", len(store.pending), maxPendingAuthorizations},
		{"codes", len(store.codes), maxAuthorizationCodes},
		{"refresh", len(store.refresh), maxRefreshTokens},
	} {
		if tc.size > tc.cap {
			t.Errorf("%s store grew to %d, past its cap of %d", tc.name, tc.size, tc.cap)
		}
	}
}

func TestRateLimiterBurstRefillAndBucketCap(t *testing.T) {
	store, clock := newTestStore()

	for i := 0; i < rateLimitCapacity; i++ {
		if !store.AllowRequest("login", "10.0.0.1") {
			t.Fatalf("request %d within the burst should be allowed", i)
		}
	}
	if store.AllowRequest("login", "10.0.0.1") {
		t.Fatal("a request past the burst should be denied")
	}

	// A different key has its own bucket.
	if !store.AllowRequest("login", "10.0.0.2") {
		t.Fatal("a different key must not inherit an exhausted bucket")
	}
	// So does a different bucket name for the same key.
	if !store.AllowRequest("register", "10.0.0.1") {
		t.Fatal("a different bucket must not inherit an exhausted one")
	}

	clock.Advance(rateLimitRefill)
	if !store.AllowRequest("login", "10.0.0.1") {
		t.Fatal("the bucket should refill one token per interval")
	}

	for i := 0; i < maxRateLimitBuckets+50; i++ {
		store.AllowRequest("probe", fmt.Sprintf("10.1.%d.%d", i/256, i%256))
		clock.Advance(time.Millisecond)
	}
	store.mu.Lock()
	size := len(store.limiters)
	store.mu.Unlock()
	if size > maxRateLimitBuckets {
		t.Errorf("the limiter's own map grew to %d, past its cap of %d", size, maxRateLimitBuckets)
	}
}

func TestNewOpaqueToken(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		token, err := newOpaqueToken()
		if err != nil {
			t.Fatalf("newOpaqueToken: %v", err)
		}
		if seen[token] {
			t.Fatalf("newOpaqueToken repeated a value after %d draws", i)
		}
		seen[token] = true

		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			t.Fatalf("token is not unpadded base64url: %v", err)
		}
		if len(raw) != 32 {
			t.Fatalf("token carries %d bytes, want 32", len(raw))
		}
	}
}

func TestStoreIsRaceFree(t *testing.T) {
	store, clock := newTestStore()
	expires := clock.Now().Add(time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("k-%02d", i)
			if err := store.RegisterClient(&Client{ClientID: id}); err != nil {
				t.Errorf("goroutine %d: RegisterClient: %v", i, err)
			}
			if err := store.PutPending(&PendingAuthorization{ID: id, CreatedAt: clock.Now(), ExpiresAt: expires}); err != nil {
				t.Errorf("goroutine %d: PutPending: %v", i, err)
			}
			if err := store.PutCode(&AuthorizationCode{Code: id, ExpiresAt: expires}); err != nil {
				t.Errorf("goroutine %d: PutCode: %v", i, err)
			}
			if err := store.PutRefreshToken(&RefreshToken{Token: id, FamilyID: "fam", ExpiresAt: expires}); err != nil {
				t.Errorf("goroutine %d: PutRefreshToken: %v", i, err)
			}
			store.GetClient(id)
			if _, ok := store.GetPending(id); !ok {
				t.Errorf("goroutine %d: GetPending(%q): not found", i, id)
			}
			store.AllowRequest("login", id)
			store.TakeCode(id)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("k-%02d", i)
		if _, ok := store.GetClient(id); !ok {
			t.Errorf("GetClient(%q): not found", id)
		}
	}
	store.RevokeFamily("fam")
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("k-%02d", i)
		if _, ok := store.ConsumeRefreshToken(id); ok {
			t.Errorf("ConsumeRefreshToken(%q): token remained after family revocation", id)
		}
	}
}
