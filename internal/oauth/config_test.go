package oauth

import (
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	hash, err := HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	base := Config{Issuer: "https://example.test", Resource: "https://example.test/mcp", LoginUser: "admin", LoginPasswordHash: hash, Signer: &testSigner{issuer: "https://example.test", audience: "https://example.test/mcp"}}
	if err := base.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if base.Scope != "time" || base.Realm != "time-mcp" || base.AccessTokenTTL != time.Hour || base.RefreshTokenTTL != 720*time.Hour || base.AuthCodeTTL != time.Minute || base.Now == nil {
		t.Fatalf("defaults = %+v", base)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty issuer", func(c *Config) { c.Issuer = "" }}, {"relative resource", func(c *Config) { c.Resource = "/mcp" }}, {"empty login user", func(c *Config) { c.LoginUser = "" }},
		{"malformed password hash", func(c *Config) { c.LoginPasswordHash = "bad" }}, {"nil signer", func(c *Config) { c.Signer = nil }}, {"short refresh", func(c *Config) { c.RefreshTokenTTL = c.AccessTokenTTL }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := base
			test.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("Validate error = nil")
			}
		})
	}
}
