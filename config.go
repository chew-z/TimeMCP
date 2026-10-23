package main

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"TimeMCP/internal/oauth"
)

// Default configuration values
const (
	// HTTP transport defaults
	defaultHTTPAddress        = ":8080"
	defaultHTTPPath           = "/mcp"
	defaultHTTPStateless      = false
	defaultHTTPHeartbeat      = 30 * time.Second
	defaultHTTPTimeout        = 30 * time.Second
	defaultHTTPCORSEnabled    = false
	defaultHTTPSessionIdleTTL = 5 * time.Minute
	// TimeMCP is always deployed behind a reverse proxy (nginx) that forwards
	// the real Host header. mcp-go's DNS-rebinding protection only guards
	// against a browser hitting a loopback-bound server directly, which
	// doesn't apply here — so it must stay off by default or every proxied
	// request 403s with "invalid Host header".
	defaultHTTPDisableLocalhostProtection = true

	// Authentication defaults
	defaultAuthEnabled  = false
	defaultAuthIssuer   = "TimeMCP"
	defaultAuthAudience = "TimeMCP-user"

	// Timezone defaults
	defaultTimezone = "" // Empty means use system timezone
)

// Config holds the server configuration
type Config struct {
	// HTTP transport settings
	HTTPAddress                    string
	HTTPPath                       string
	HTTPStateless                  bool
	HTTPHeartbeat                  time.Duration
	HTTPTimeout                    time.Duration
	HTTPCORSEnabled                bool
	HTTPCORSOrigins                []string
	HTTPSessionIdleTTL             time.Duration
	HTTPDisableLocalhostProtection bool

	// Authentication settings
	AuthEnabled   bool
	AuthSecretKey string
	AuthIssuer    string
	AuthAudience  string

	// OAuth settings
	OAuthEnabled           bool
	OAuthIssuer            string
	OAuthResource          string
	OAuthLoginUser         string
	OAuthLoginPasswordHash string
	OAuthAccessTTL         time.Duration
	OAuthRefreshTTL        time.Duration

	// Timezone settings
	DefaultTimezone string
}

// NewConfig creates a new configuration from environment variables
func NewConfig() (*Config, error) {
	httpAddress, httpPath, httpStateless, httpHeartbeat, httpTimeout, httpSessionIdleTTL, httpDisableLocalhostProtection := parseHTTPSettings()
	authEnabled, authSecretKey, authIssuer, authAudience, err := parseAuthSettings()
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err = parseOAuthSettings(cfg); err != nil {
		return nil, fmt.Errorf("parsing OAuth settings: %w", err)
	}
	if cfg.OAuthEnabled {
		authEnabled = true
		if authSecretKey == "" {
			return nil, fmt.Errorf("TIME_AUTH_SECRET_KEY is required when TIME_OAUTH_ENABLED=true")
		}
		if len(authSecretKey) < 32 {
			return nil, fmt.Errorf("TIME_AUTH_SECRET_KEY must be at least 32 bytes when TIME_OAUTH_ENABLED=true")
		}
		cfg.AuthSecretKey = authSecretKey
		if _, err = cfg.OAuthConfig(); err != nil {
			return nil, fmt.Errorf("validating OAuth settings: %w", err)
		}
	}
	httpCORSEnabled, httpCORSOrigins, err := parseCORSSettings(authEnabled)
	if err != nil {
		return nil, err
	}
	defaultTimezone, err := parseTimezoneSettings()
	if err != nil {
		return nil, err
	}

	cfg.HTTPAddress = httpAddress
	cfg.HTTPPath = httpPath
	cfg.HTTPStateless = httpStateless
	cfg.HTTPHeartbeat = httpHeartbeat
	cfg.HTTPTimeout = httpTimeout
	cfg.HTTPCORSEnabled = httpCORSEnabled
	cfg.HTTPCORSOrigins = httpCORSOrigins
	cfg.HTTPSessionIdleTTL = httpSessionIdleTTL
	cfg.HTTPDisableLocalhostProtection = httpDisableLocalhostProtection
	cfg.AuthEnabled = authEnabled
	cfg.AuthSecretKey = authSecretKey
	cfg.AuthIssuer = authIssuer
	cfg.AuthAudience = authAudience
	cfg.DefaultTimezone = defaultTimezone
	return cfg, nil
}

// parseOAuthSettings reads opt-in OAuth settings without changing the legacy parsers.
func parseOAuthSettings(c *Config) error {
	enabled, err := parseStrictEnvBool("TIME_OAUTH_ENABLED", false)
	if err != nil {
		return err
	}
	c.OAuthEnabled = enabled
	c.OAuthIssuer = os.Getenv("TIME_OAUTH_ISSUER")
	c.OAuthResource = os.Getenv("TIME_OAUTH_RESOURCE")
	c.OAuthLoginUser = os.Getenv("TIME_OAUTH_LOGIN_USER")
	c.OAuthLoginPasswordHash = os.Getenv("TIME_OAUTH_LOGIN_PASSWORD_HASH")
	c.OAuthAccessTTL, err = parseStrictEnvDuration("TIME_OAUTH_ACCESS_TTL", time.Hour)
	if err != nil {
		return err
	}
	c.OAuthRefreshTTL, err = parseStrictEnvDuration("TIME_OAUTH_REFRESH_TTL", 720*time.Hour)
	return err
}

// OAuthConfig builds and validates the OAuth authorization-server settings.
func (c *Config) OAuthConfig() (oauth.Config, error) {
	for _, setting := range []struct{ name, value string }{
		{"TIME_OAUTH_ISSUER", c.OAuthIssuer},
		{"TIME_OAUTH_RESOURCE", c.OAuthResource},
		{"TIME_OAUTH_LOGIN_USER", c.OAuthLoginUser},
		{"TIME_OAUTH_LOGIN_PASSWORD_HASH", c.OAuthLoginPasswordHash},
	} {
		if setting.value == "" {
			return oauth.Config{}, fmt.Errorf("%s is required when OAuth is enabled", setting.name)
		}
	}
	for _, setting := range []struct {
		name  string
		value time.Duration
	}{
		{"TIME_OAUTH_ACCESS_TTL", c.OAuthAccessTTL},
		{"TIME_OAUTH_REFRESH_TTL", c.OAuthRefreshTTL},
	} {
		if setting.value <= 0 {
			return oauth.Config{}, fmt.Errorf("%s must be positive", setting.name)
		}
	}
	cfg := oauth.Config{
		Issuer: c.OAuthIssuer, Resource: c.OAuthResource,
		LoginUser: c.OAuthLoginUser, LoginPasswordHash: c.OAuthLoginPasswordHash,
		AccessTokenTTL: c.OAuthAccessTTL, RefreshTokenTTL: c.OAuthRefreshTTL,
		Signer: HS256Signer{Secret: c.AuthSecretKey, Issuer: c.OAuthIssuer, Audience: c.OAuthResource},
	}
	if err := cfg.Validate(); err != nil {
		return oauth.Config{}, fmt.Errorf(
			"validating OAuth config (TIME_OAUTH_ISSUER, TIME_OAUTH_RESOURCE, "+
				"TIME_OAUTH_LOGIN_PASSWORD_HASH, TIME_OAUTH_ACCESS_TTL, TIME_OAUTH_REFRESH_TTL): %w",
			err,
		)
	}
	return cfg, nil
}

// parseStrictEnvBool uses the fallback for unset or empty values and rejects invalid nonempty values.
func parseStrictEnvBool(key string, fallback bool) (bool, error) {
	value, present := os.LookupEnv(key)
	if !present || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s: invalid boolean: %w", key, err)
	}
	return parsed, nil
}

// parseStrictEnvDuration uses the fallback for unset or empty values and rejects invalid nonempty values.
func parseStrictEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	value, present := os.LookupEnv(key)
	if !present || value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration: %w", key, err)
	}
	return parsed, nil
}

func parseHTTPSettings() (string, string, bool, time.Duration, time.Duration, time.Duration, bool) {
	httpAddress := getEnvWithDefault("TIME_HTTP_ADDRESS", defaultHTTPAddress)
	httpPath := getEnvWithDefault("TIME_HTTP_PATH", defaultHTTPPath)
	httpStateless := parseEnvBool("TIME_HTTP_STATELESS", defaultHTTPStateless)
	httpHeartbeat := parseEnvDuration("TIME_HTTP_HEARTBEAT", defaultHTTPHeartbeat)
	httpTimeout := parseEnvDuration("TIME_HTTP_TIMEOUT", defaultHTTPTimeout)
	httpSessionIdleTTL := parseEnvDuration("TIME_HTTP_SESSION_IDLE_TTL", defaultHTTPSessionIdleTTL)
	httpDisableLocalhostProtection := parseEnvBool("TIME_HTTP_DISABLE_LOCALHOST_PROTECTION", defaultHTTPDisableLocalhostProtection)
	return httpAddress, httpPath, httpStateless, httpHeartbeat, httpTimeout, httpSessionIdleTTL, httpDisableLocalhostProtection
}

func parseAuthSettings() (bool, string, string, string, error) {
	authEnabled := parseEnvBool("TIME_AUTH_ENABLED", defaultAuthEnabled)
	authSecretKey := os.Getenv("TIME_AUTH_SECRET_KEY")
	authIssuer := getEnvWithDefault("TIME_AUTH_ISSUER", defaultAuthIssuer)
	authAudience := getEnvWithDefault("TIME_AUTH_AUDIENCE", defaultAuthAudience)

	if authEnabled && authSecretKey == "" {
		return false, "", "", "", fmt.Errorf("TIME_AUTH_SECRET_KEY is required when TIME_AUTH_ENABLED=true")
	}
	if authEnabled && len(authSecretKey) < 32 {
		slog.Warn("TIME_AUTH_SECRET_KEY should be at least 32 characters for security")
	}
	return authEnabled, authSecretKey, authIssuer, authAudience, nil
}

func parseCORSOrigins(originsStr string) []string {
	if originsStr == "" {
		return nil
	}

	parts := strings.Split(originsStr, ",")
	seen := make(map[string]struct{})
	httpCORSOrigins := make([]string, 0, len(parts))

	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}

		addOrigin := func(origin string) {
			if _, ok := seen[origin]; !ok {
				httpCORSOrigins = append(httpCORSOrigins, origin)
				seen[origin] = struct{}{}
			}
		}

		if trimmed == "*" {
			addOrigin("*")
			continue
		}

		if strings.Contains(trimmed, "://") {
			u, err := url.Parse(trimmed)
			if err == nil && u.Host != "" {
				addOrigin(u.Host)
			} else {
				slog.Warn("invalid CORS origin URL", "origin", trimmed)
			}
			continue
		}
		addOrigin(trimmed)
	}
	return httpCORSOrigins
}

func parseCORSSettings(authEnabled bool) (bool, []string, error) {
	httpCORSEnabled := parseEnvBool("TIME_HTTP_CORS_ENABLED", defaultHTTPCORSEnabled)
	originsStr := os.Getenv("TIME_HTTP_CORS_ORIGINS")
	httpCORSOrigins := parseCORSOrigins(originsStr)

	// Harden: do NOT default to "*"; empty means no origins allowed.
	if httpCORSEnabled && authEnabled {
		if slices.Contains(httpCORSOrigins, "*") {
			return false, nil, fmt.Errorf("insecure CORS: TIME_HTTP_CORS_ORIGINS contains \"*\" while TIME_AUTH_ENABLED=true")
		}
	}
	return httpCORSEnabled, httpCORSOrigins, nil
}

func parseTimezoneSettings() (string, error) {
	defaultTimezone := getEnvWithDefault("TIME_DEFAULT_TIMEZONE", defaultTimezone)

	if defaultTimezone != "" {
		if _, err := time.LoadLocation(defaultTimezone); err != nil {
			return "", fmt.Errorf("invalid TIME_DEFAULT_TIMEZONE: %s (%v)", defaultTimezone, err)
		}
		slog.Info("using default timezone", "timezone", defaultTimezone)
	}
	return defaultTimezone, nil
}

// Helper functions for parsing environment variables

func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseEnvBool(key string, defaultValue bool) bool {
	if str := os.Getenv(key); str != "" {
		if val, err := strconv.ParseBool(str); err == nil {
			return val
		}
		slog.Warn("invalid boolean env value", "key", key, "value", str, "default", defaultValue)
	}
	return defaultValue
}

func parseEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if str := os.Getenv(key); str != "" {
		if val, err := time.ParseDuration(str); err == nil {
			return val
		}
		slog.Warn("invalid duration env value", "key", key, "value", str, "default", defaultValue.String())
	}
	return defaultValue
}
