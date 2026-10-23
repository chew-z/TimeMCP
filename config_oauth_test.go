package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"TimeMCP/internal/oauth"
)

func oauthTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TIME_OAUTH_ENABLED", "TIME_OAUTH_ISSUER", "TIME_OAUTH_RESOURCE",
		"TIME_OAUTH_LOGIN_USER", "TIME_OAUTH_LOGIN_PASSWORD_HASH",
		"TIME_OAUTH_ACCESS_TTL", "TIME_OAUTH_REFRESH_TTL",
	} {
		t.Setenv(key, "") // Register restoration before unsetting the variable.
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unsetting %s: %v", key, err)
		}
	}
	for key, value := range map[string]string{
		"TIME_AUTH_ENABLED": "false", "TIME_AUTH_SECRET_KEY": "",
		"TIME_AUTH_ISSUER": "", "TIME_AUTH_AUDIENCE": "",
		"TIME_HTTP_CORS_ENABLED": "false", "TIME_HTTP_CORS_ORIGINS": "",
		"TIME_DEFAULT_TIMEZONE": "",
	} {
		t.Setenv(key, value)
	}
}

func validOAuthTestEnv(t *testing.T) {
	t.Helper()
	oauthTestEnv(t)
	hash, err := oauth.HashPassword("pw")
	if err != nil {
		t.Fatalf("hashing OAuth password: %v", err)
	}
	for key, value := range map[string]string{
		"TIME_OAUTH_ENABLED":             "true",
		"TIME_OAUTH_ISSUER":              "https://karma.example/time",
		"TIME_OAUTH_RESOURCE":            "https://karma.example/time/mcp",
		"TIME_OAUTH_LOGIN_USER":          "tester",
		"TIME_OAUTH_LOGIN_PASSWORD_HASH": hash,
		"TIME_AUTH_SECRET_KEY":           strings.Repeat("s", 32),
	} {
		t.Setenv(key, value)
	}
}

func TestOAuthDisabled(t *testing.T) {
	oauthTestEnv(t)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.OAuthEnabled || cfg.AuthEnabled || cfg.AuthIssuer != defaultAuthIssuer || cfg.AuthAudience != defaultAuthAudience {
		t.Fatal("disabled OAuth changed authentication defaults")
	}
	t.Setenv("TIME_OAUTH_ENABLED", "false")
	explicit, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig with explicit opt-out: %v", err)
	}
	if !reflect.DeepEqual(cfg, explicit) {
		t.Fatal("explicit opt-out differs from unset OAuth")
	}
	// Existing static-JWT deployments still accept a short secret.
	t.Setenv("TIME_AUTH_ENABLED", "true")
	t.Setenv("TIME_AUTH_SECRET_KEY", "short")
	if _, err := NewConfig(); err != nil {
		t.Fatalf("legacy short-secret behavior changed: %v", err)
	}
}

func TestOAuthEnabled(t *testing.T) {
	validOAuthTestEnv(t)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if !cfg.OAuthEnabled || !cfg.AuthEnabled || cfg.OAuthAccessTTL != time.Hour || cfg.OAuthRefreshTTL != 720*time.Hour {
		t.Fatal("OAuth did not enable auth with default TTLs")
	}
	oc, err := cfg.OAuthConfig()
	if err != nil {
		t.Fatalf("OAuthConfig: %v", err)
	}
	if oc.Scope != "time" || oc.Realm != "time-mcp" || oc.Issuer != cfg.OAuthIssuer || oc.Resource != cfg.OAuthResource || oc.LoginUser != cfg.OAuthLoginUser || oc.LoginPasswordHash != cfg.OAuthLoginPasswordHash {
		t.Fatal("incorrect OAuth configuration")
	}
	signer, ok := oc.Signer.(HS256Signer)
	if !ok || signer.Secret != cfg.AuthSecretKey || signer.Issuer != cfg.OAuthIssuer || signer.Audience != cfg.OAuthResource {
		t.Fatal("incorrect OAuth signer configuration")
	}
	t.Setenv("TIME_OAUTH_ACCESS_TTL", "2h")
	t.Setenv("TIME_OAUTH_REFRESH_TTL", "48h")
	custom, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig with custom TTLs: %v", err)
	}
	if custom.OAuthAccessTTL != 2*time.Hour || custom.OAuthRefreshTTL != 48*time.Hour {
		t.Fatal("custom TTLs were not applied")
	}
}

func TestOAuthInvalidConfig(t *testing.T) {
	for _, tc := range []struct{ name, key, value, errorKey string }{
		{"missing issuer", "TIME_OAUTH_ISSUER", "", "TIME_OAUTH_ISSUER"},
		{"missing resource", "TIME_OAUTH_RESOURCE", "", "TIME_OAUTH_RESOURCE"},
		{"missing user", "TIME_OAUTH_LOGIN_USER", "", "TIME_OAUTH_LOGIN_USER"},
		{"missing hash", "TIME_OAUTH_LOGIN_PASSWORD_HASH", "", "TIME_OAUTH_LOGIN_PASSWORD_HASH"},
		{"invalid issuer", "TIME_OAUTH_ISSUER", "/time", "TIME_OAUTH_ISSUER"},
		{"invalid resource", "TIME_OAUTH_RESOURCE", "/mcp", "TIME_OAUTH_RESOURCE"},
		{"invalid hash", "TIME_OAUTH_LOGIN_PASSWORD_HASH", "plaintext", "TIME_OAUTH_LOGIN_PASSWORD_HASH"},
		{"short secret", "TIME_AUTH_SECRET_KEY", strings.Repeat("s", 31), "TIME_AUTH_SECRET_KEY"},
		{"empty secret", "TIME_AUTH_SECRET_KEY", "", "TIME_AUTH_SECRET_KEY"},
		{"invalid boolean", "TIME_OAUTH_ENABLED", "tru", "TIME_OAUTH_ENABLED"},
		{"invalid access TTL", "TIME_OAUTH_ACCESS_TTL", "soon", "TIME_OAUTH_ACCESS_TTL"},
		{"zero access TTL", "TIME_OAUTH_ACCESS_TTL", "0s", "TIME_OAUTH_ACCESS_TTL"},
		{"negative access TTL", "TIME_OAUTH_ACCESS_TTL", "-1h", "TIME_OAUTH_ACCESS_TTL"},
		{"invalid refresh TTL", "TIME_OAUTH_REFRESH_TTL", "later", "TIME_OAUTH_REFRESH_TTL"},
		{"negative refresh TTL", "TIME_OAUTH_REFRESH_TTL", "-1h", "TIME_OAUTH_REFRESH_TTL"},
		{"equal TTLs", "TIME_OAUTH_REFRESH_TTL", "1h", "TIME_OAUTH_REFRESH_TTL"},
		{"refresh below access", "TIME_OAUTH_REFRESH_TTL", "30m", "TIME_OAUTH_REFRESH_TTL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validOAuthTestEnv(t)
			t.Setenv(tc.key, tc.value)
			if _, err := NewConfig(); err == nil || !strings.Contains(err.Error(), tc.errorKey) {
				t.Fatalf("expected error naming %s, got %v", tc.errorKey, err)
			}
		})
	}
}

func TestOAuthRejectsCORSStar(t *testing.T) {
	validOAuthTestEnv(t)
	t.Setenv("TIME_HTTP_CORS_ENABLED", "true")
	t.Setenv("TIME_HTTP_CORS_ORIGINS", "*")
	if _, err := NewConfig(); err == nil || !strings.Contains(err.Error(), "TIME_HTTP_CORS_ORIGINS") {
		t.Fatalf("expected CORS wildcard error, got %v", err)
	}
}

func TestOAuthStrictTTLsWhenDisabled(t *testing.T) {
	for _, key := range []string{"TIME_OAUTH_ACCESS_TTL", "TIME_OAUTH_REFRESH_TTL"} {
		t.Run(key, func(t *testing.T) {
			oauthTestEnv(t)
			t.Setenv(key, "bad")
			if _, err := NewConfig(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected strict parsing error for %s, got %v", key, err)
			}
		})
	}
}

func TestHS256Signer(t *testing.T) {
	signer := HS256Signer{Secret: strings.Repeat("s", 32), Issuer: "https://karma.example/time", Audience: "https://karma.example/time/mcp"}
	auth, err := NewAuthMiddleware(signer.Secret, true, signer.Issuer, signer.Audience, nil)
	if err != nil {
		t.Fatalf("creating auth middleware: %v", err)
	}
	before := time.Now().Truncate(time.Second)
	token, err := signer.Sign("tester", "time", time.Hour)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	claims, err := auth.validateJWT(token)
	if err != nil {
		t.Fatalf("validating signed token: %v", err)
	}
	if claims.UserID != "tester" || claims.Username != "tester" || claims.Role != "oauth" || claims.Scope != "time" {
		t.Fatalf("incorrect identity/scope claims: %+v", claims)
	}
	if claims.Issuer != signer.Issuer || len(claims.Audience) != 1 || claims.Audience[0] != signer.Audience {
		t.Fatal("incorrect issuer/audience claims")
	}
	if claims.IssuedAt == nil || claims.NotBefore == nil || claims.ExpiresAt == nil {
		t.Fatal("missing temporal claims")
	}
	if claims.IssuedAt.Time.Before(before) || claims.IssuedAt.Time.After(time.Now()) || !claims.NotBefore.Equal(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != time.Hour {
		t.Fatal("incorrect token timestamps")
	}
	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, err := signer.Sign("tester", "time", ttl); err == nil {
			t.Errorf("expected error for TTL %v", ttl)
		}
	}
}

// TestOAuthEmptyEnv verifies compose-style empty variables use the unset defaults.
func TestOAuthEmptyEnv(t *testing.T) {
	oauthTestEnv(t)
	baseline, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	for _, key := range []string{"TIME_OAUTH_ENABLED", "TIME_OAUTH_ACCESS_TTL", "TIME_OAUTH_REFRESH_TTL"} {
		t.Setenv(key, "")
	}
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig with empty settings: %v", err)
	}
	if !reflect.DeepEqual(baseline, cfg) {
		t.Fatal("empty OAuth settings differ from unset defaults")
	}
	validOAuthTestEnv(t)
	t.Setenv("TIME_OAUTH_ACCESS_TTL", "")
	t.Setenv("TIME_OAUTH_REFRESH_TTL", "")
	cfg, err = NewConfig()
	if err != nil {
		t.Fatalf("NewConfig with enabled OAuth and empty TTLs: %v", err)
	}
	if !cfg.OAuthEnabled || cfg.OAuthAccessTTL != time.Hour || cfg.OAuthRefreshTTL != 720*time.Hour {
		t.Fatal("empty TTLs did not use defaults with OAuth enabled")
	}
}
