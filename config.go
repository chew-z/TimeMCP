package main

import (
	"fmt"
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
	defaultHTTPCORSEnabled = true

	// Authentication defaults
	defaultAuthEnabled = false
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
}

// NewConfig creates a new configuration from environment variables
func NewConfig() (*Config, error) {
	// HTTP transport settings
	httpAddress := getEnvWithDefault("TIME_HTTP_ADDRESS", defaultHTTPAddress)
	httpPath := getEnvWithDefault("TIME_HTTP_PATH", defaultHTTPPath)
	httpStateless := parseEnvBool("TIME_HTTP_STATELESS", defaultHTTPStateless)
	httpHeartbeat := parseEnvDuration("TIME_HTTP_HEARTBEAT", defaultHTTPHeartbeat)
	httpTimeout := parseEnvDuration("TIME_HTTP_TIMEOUT", defaultHTTPTimeout)
	httpCORSEnabled := parseEnvBool("TIME_HTTP_CORS_ENABLED", defaultHTTPCORSEnabled)

	// Parse CORS origins
	var httpCORSOrigins []string
	if originsStr := os.Getenv("TIME_HTTP_CORS_ORIGINS"); originsStr != "" {
		parts := strings.Split(originsStr, ",")
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				httpCORSOrigins = append(httpCORSOrigins, trimmed)
			}
		}
	}
	if len(httpCORSOrigins) == 0 {
		httpCORSOrigins = []string{"*"} // Default allow all origins
	}

	// Authentication settings
	authEnabled := parseEnvBool("TIME_AUTH_ENABLED", defaultAuthEnabled)
	authSecretKey := os.Getenv("TIME_AUTH_SECRET_KEY")

	// If authentication is enabled, require secret key
	if authEnabled && authSecretKey == "" {
		return nil, fmt.Errorf("TIME_AUTH_SECRET_KEY is required when TIME_AUTH_ENABLED=true")
	}

	// Warn if secret key is too short (for security)
	if authEnabled && len(authSecretKey) < 32 {
		fmt.Fprintf(os.Stderr, "[WARN] TIME_AUTH_SECRET_KEY should be at least 32 characters for security\n")
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
	}, nil
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