// Package oauth implements the TimeMCP OAuth 2.1 authorization server.
package oauth

import (
	"fmt"
	"net/url"
	"time"
)

// Signer mints an access token for an authenticated user and scope.
type Signer interface {
	Sign(username, scope string, ttl time.Duration) (string, error)
}

// Config configures the authorization and resource server metadata.
type Config struct {
	Issuer, Resource                             string
	LoginUser, LoginPasswordHash                 string
	AccessTokenTTL, RefreshTokenTTL, AuthCodeTTL time.Duration
	Scope, Realm                                 string
	Signer                                       Signer
	Now                                          func() time.Time
}

// Validate fills defaults and verifies the OAuth configuration.
//
//nolint:gocyclo // shared with Pushover/Alerts/Answer internal/oauth; keep in sync
func (c *Config) Validate() error {
	if c.AccessTokenTTL == 0 {
		c.AccessTokenTTL = time.Hour
	}
	if c.RefreshTokenTTL == 0 {
		c.RefreshTokenTTL = 720 * time.Hour
	}
	if c.AuthCodeTTL == 0 {
		c.AuthCodeTTL = time.Minute
	}
	if c.Scope == "" {
		c.Scope = "time"
	}
	if c.Realm == "" {
		c.Realm = "time-mcp"
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if !isAbsoluteURL(c.Issuer) {
		return fmt.Errorf("issuer must be an absolute URL")
	}
	if !isAbsoluteURL(c.Resource) {
		return fmt.Errorf("resource must be an absolute URL")
	}
	if c.LoginUser == "" {
		return fmt.Errorf("LoginUser must not be empty")
	}
	if err := ParsePasswordHash(c.LoginPasswordHash); err != nil {
		return fmt.Errorf("LoginPasswordHash: %w", err)
	}
	if c.Signer == nil {
		return fmt.Errorf("Signer must not be nil")
	}
	if c.RefreshTokenTTL <= c.AccessTokenTTL {
		return fmt.Errorf("RefreshTokenTTL must be greater than AccessTokenTTL")
	}
	return nil
}

func isAbsoluteURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}

func (c Config) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}
