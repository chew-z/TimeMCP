package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Default configuration values
const (
	// HTTP transport defaults
	defaultHTTPAddress     = ":8080"
	defaultHTTPPath        = "/mcp"
	defaultHTTPStateless   = false
	defaultHTTPHeartbeat   = 30 * time.Second
	defaultHTTPTimeout     = 30 * time.Second
	defaultHTTPCORSEnabled = false

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
	HTTPAddress     string
	HTTPPath        string
	HTTPStateless   bool
	HTTPHeartbeat   time.Duration
	HTTPTimeout     time.Duration
	HTTPCORSEnabled bool
	HTTPCORSOrigins []string

	// Authentication settings
	AuthEnabled   bool
	AuthSecretKey string
	AuthIssuer    string
	AuthAudience  string

	// Timezone settings
	DefaultTimezone string
}

// NewConfig creates a new configuration from environment variables
func NewConfig() (*Config, error) {
	httpAddress, httpPath, httpStateless, httpHeartbeat, httpTimeout := parseHTTPSettings()
	authEnabled, authSecretKey, authIssuer, authAudience, err := parseAuthSettings()
	if err != nil {
		return nil, err
	}
	httpCORSEnabled, httpCORSOrigins, err := parseCORSSettings(authEnabled)
	if err != nil {
		return nil, err
	}
	defaultTimezone, err := parseTimezoneSettings()
	if err != nil {
		return nil, err
	}

	return &Config{
		HTTPAddress:     httpAddress,
		HTTPPath:        httpPath,
		HTTPStateless:   httpStateless,
		HTTPHeartbeat:   httpHeartbeat,
		HTTPTimeout:     httpTimeout,
		HTTPCORSEnabled: httpCORSEnabled,
		HTTPCORSOrigins: httpCORSOrigins,
		AuthEnabled:     authEnabled,
		AuthSecretKey:   authSecretKey,
		AuthIssuer:      authIssuer,
		AuthAudience:    authAudience,
		DefaultTimezone: defaultTimezone,
	}, nil
}

func parseHTTPSettings() (string, string, bool, time.Duration, time.Duration) {
	httpAddress := getEnvWithDefault("TIME_HTTP_ADDRESS", defaultHTTPAddress)
	httpPath := getEnvWithDefault("TIME_HTTP_PATH", defaultHTTPPath)
	httpStateless := parseEnvBool("TIME_HTTP_STATELESS", defaultHTTPStateless)
	httpHeartbeat := parseEnvDuration("TIME_HTTP_HEARTBEAT", defaultHTTPHeartbeat)
	httpTimeout := parseEnvDuration("TIME_HTTP_TIMEOUT", defaultHTTPTimeout)
	return httpAddress, httpPath, httpStateless, httpHeartbeat, httpTimeout
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
		fmt.Fprintf(os.Stderr, "[WARN] TIME_AUTH_SECRET_KEY should be at least 32 characters for security\n")
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
				fmt.Fprintf(os.Stderr, "[WARN] Invalid CORS origin URL: %q (skipping)\n", trimmed)
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
		for _, o := range httpCORSOrigins {
			if o == "*" {
				return false, nil, fmt.Errorf("insecure CORS: TIME_HTTP_CORS_ORIGINS contains \"*\" while TIME_AUTH_ENABLED=true")
			}
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
		fmt.Fprintf(os.Stderr, "[INFO] Using default timezone: %s\n", defaultTimezone)
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
		fmt.Fprintf(os.Stderr, "[WARN] Invalid boolean value for %s: %q. Using default: %t\n", key, str, defaultValue)
	}
	return defaultValue
}

func parseEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if str := os.Getenv(key); str != "" {
		if val, err := time.ParseDuration(str); err == nil {
			return val
		}
		fmt.Fprintf(os.Stderr, "[WARN] Invalid duration value for %s: %q. Using default: %s\n", key, str, defaultValue.String())
	}
	return defaultValue
}
