package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mark3labs/mcp-go/server"
)

// AuthMiddleware handles JWT-based authentication for HTTP transport
type AuthMiddleware struct {
	secretKey []byte
	enabled   bool
}

// Claims represents JWT token claims
type Claims struct {
	jwt.RegisteredClaims
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(secretKey string, enabled bool) *AuthMiddleware {
	return &AuthMiddleware{
		secretKey: []byte(secretKey),
		enabled:   enabled,
	}
}

// HTTPContextFunc returns a middleware function compatible with mcp-go
func (a *AuthMiddleware) HTTPContextFunc(next func(ctx context.Context, r *http.Request) context.Context) func(ctx context.Context, r *http.Request) context.Context {
	return func(ctx context.Context, r *http.Request) context.Context {
		// If authentication is disabled, just call the next middleware
		if !a.enabled {
			return next(ctx, r)
		}

		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		parts := strings.Fields(authHeader)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			fmt.Printf("Missing or invalid authorization header from %s\n", r.RemoteAddr)
			// Set authentication error in context instead of failing the request
			ctx = context.WithValue(ctx, "auth_error", "missing_token")
			return next(ctx, r)
		}

		token := parts[1]

		// Validate JWT token
		claims, err := a.validateJWT(token)
		if err != nil {
			fmt.Printf("Invalid token from %s: %v\n", r.RemoteAddr, err)
			errorKey := "invalid_token"
			if err == jwt.ErrTokenExpired {
				errorKey = "expired_token"
			}
			ctx = context.WithValue(ctx, "auth_error", errorKey)
			return next(ctx, r)
		}

		fmt.Printf("Authenticated user %s (%s) from %s\n", claims.Username, claims.Role, r.RemoteAddr)

		// Add user to request context
		ctx = context.WithValue(ctx, "authenticated", true)
		ctx = context.WithValue(ctx, "user_id", claims.UserID)
		ctx = context.WithValue(ctx, "username", claims.Username)
		ctx = context.WithValue(ctx, "user_role", claims.Role)

		return next(ctx, r)
	}
}

// validateJWT validates a JWT token and returns the claims
func (a *AuthMiddleware) validateJWT(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.secretKey, nil
	},
		jwt.WithIssuer("pushover-mcp"),
		jwt.WithAudience("pushover-mcp-user"),
		jwt.WithLeeway(60*time.Second),
	)

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// GenerateToken generates a JWT token for a user (utility function for testing/setup)
func (a *AuthMiddleware) GenerateToken(userID, username, role string, expirationHours int) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "pushover-mcp",
			Audience:  jwt.ClaimStrings{"pushover-mcp-user"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expirationHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
		UserID:   userID,
		Username: username,
		Role:     role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.secretKey)
}

// isAuthenticated checks if the request context contains valid authentication
func isAuthenticated(ctx context.Context) bool {
	if auth, ok := ctx.Value("authenticated").(bool); ok && auth {
		return true
	}
	return false
}

// getAuthError returns any authentication error from the context
func getAuthError(ctx context.Context) string {
	if err, ok := ctx.Value("auth_error").(string); ok {
		return err
	}
	return ""
}

// getUserInfo extracts user information from the authenticated context
func getUserInfo(ctx context.Context) (userID, username, role string) {
	if userID, ok := ctx.Value("user_id").(string); ok {
		if username, ok := ctx.Value("username").(string); ok {
			if role, ok := ctx.Value("user_role").(string); ok {
				return userID, username, role
			}
		}
	}
	return "", "", ""
}

// CreateTokenCommand creates a command-line utility to generate tokens
func CreateTokenCommand(secretKey, userID, username, role string, expirationHours int) {
	if secretKey == "" {
		fmt.Fprintln(os.Stderr, "Error: TIME_AUTH_SECRET_KEY environment variable is required")
		return
	}

	auth := NewAuthMiddleware(secretKey, true)

	token, err := auth.GenerateToken(userID, username, role, expirationHours)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating token: %v\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "Generated JWT token:\n%s\n\n", token)
	fmt.Fprintf(os.Stderr, "Token details:\n")
	fmt.Fprintf(os.Stderr, "  User ID: %s\n", userID)
	fmt.Fprintf(os.Stderr, "  Username: %s\n", username)
	fmt.Fprintf(os.Stderr, "  Role: %s\n", role)
	fmt.Fprintf(os.Stderr, "  Expires: %s\n", time.Now().Add(time.Duration(expirationHours)*time.Hour).Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "\nTo use this token, include it in HTTP requests:\n")
	fmt.Fprintf(os.Stderr, "  Authorization: Bearer %s\n", token)
}

// createHTTPMiddleware creates an HTTP context function with CORS, logging, and authentication
func createHTTPMiddleware(config *Config) server.HTTPContextFunc {
	// Create authentication middleware
	var authMiddleware *AuthMiddleware
	if config.AuthEnabled {
		authMiddleware = NewAuthMiddleware(config.AuthSecretKey, config.AuthEnabled)
		fmt.Println("HTTP authentication enabled")
	}

	return func(ctx context.Context, r *http.Request) context.Context {
		// Log HTTP request
		fmt.Printf("HTTP %s %s from %s\n", r.Method, r.URL.Path, r.RemoteAddr)

		// Apply authentication middleware if enabled
		if authMiddleware != nil {
			// Create a wrapper function for the next middleware step
			nextFunc := func(ctx context.Context, r *http.Request) context.Context {
				return ctx
			}
			// Apply authentication middleware
			ctx = authMiddleware.HTTPContextFunc(nextFunc)(ctx, r)
		}

		// Add CORS headers if enabled
		if config.HTTPCORSEnabled {
			// Check if request origin is allowed
			origin := r.Header.Get("Origin")
			if origin != "" && isOriginAllowed(origin, config.HTTPCORSOrigins) {
				fmt.Printf("CORS: Origin %s is allowed\n", origin)
			}
		}

		// Add request info to context
		ctx = context.WithValue(ctx, "http_method", r.Method)
		ctx = context.WithValue(ctx, "http_path", r.URL.Path)
		ctx = context.WithValue(ctx, "http_remote_addr", r.RemoteAddr)

		return ctx
	}
}

// isOriginAllowed checks if the origin is in the allowed list
func isOriginAllowed(origin string, allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
		// Support wildcard subdomains (e.g., "*.example.com")
		if domain, found := strings.CutPrefix(allowed, "*."); found {
			if strings.HasSuffix(origin, domain) {
				return true
			}
		}
	}
	return false
}
