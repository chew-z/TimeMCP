package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mark3labs/mcp-go/server"
)

type contextKey string

const (
	authenticatedKey  contextKey = "authenticated"
	authErrorKey      contextKey = "auth_error"
	userIDKey         contextKey = "user_id"
	usernameKey       contextKey = "username"
	userRoleKey       contextKey = "user_role"
	httpMethodKey     contextKey = "http_method"
	httpPathKey       contextKey = "http_path"
	httpRemoteAddrKey contextKey = "http_remote_addr"
)

const (
	authErrorMissingToken = "missing_token"
	authErrorInvalidToken = "invalid_token"
	authErrorExpiredToken = "expired_token"
)

type httpMiddleware func(ctx context.Context, r *http.Request) context.Context

// AuthMiddleware handles JWT-based authentication for HTTP transport
type AuthMiddleware struct {
	secretKey []byte
	enabled   bool
	issuer    string
	audience  string
}

// Claims represents JWT token claims
type Claims struct {
	jwt.RegisteredClaims
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(secretKey string, enabled bool, issuer string, audience string) (*AuthMiddleware, error) {
	if enabled && secretKey == "" {
		return nil, fmt.Errorf("auth enabled but secret key is empty")
	}
	return &AuthMiddleware{
		secretKey: []byte(secretKey),
		enabled:   enabled,
		issuer:    issuer,
		audience:  audience,
	}, nil
}

// HTTPContextFunc returns a middleware function compatible with mcp-go
func (a *AuthMiddleware) HTTPContextFunc(next httpMiddleware) httpMiddleware {
	return func(ctx context.Context, r *http.Request) context.Context {
		// If authentication is disabled, just call the next middleware
		if !a.enabled {
			return next(ctx, r)
		}

		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		parts := strings.Fields(authHeader)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			log.Printf("Missing or invalid authorization header from %s\n", r.RemoteAddr)
			// Set authentication error in context instead of failing the request
			ctx = context.WithValue(ctx, authErrorKey, authErrorMissingToken)
			ctx = context.WithValue(ctx, authenticatedKey, false)
			return next(ctx, r)
		}

		token := parts[1]

		// Validate JWT token
		claims, err := a.validateJWT(token)
		if err != nil {
			log.Printf("Invalid token from %s: %v\n", r.RemoteAddr, err)
			errorKey := authErrorInvalidToken
			if errors.Is(err, jwt.ErrTokenExpired) {
				errorKey = authErrorExpiredToken
			}
			ctx = context.WithValue(ctx, authErrorKey, errorKey)
			ctx = context.WithValue(ctx, authenticatedKey, false)
			return next(ctx, r)
		}

		log.Printf("Authenticated user %s (%s) from %s\n", claims.Username, claims.Role, r.RemoteAddr)

		// Add user to request context
		ctx = context.WithValue(ctx, authenticatedKey, true)
		ctx = context.WithValue(ctx, userIDKey, claims.UserID)
		ctx = context.WithValue(ctx, usernameKey, claims.Username)
		ctx = context.WithValue(ctx, userRoleKey, claims.Role)

		return next(ctx, r)
	}
}

// validateJWT validates a JWT token and returns the claims
func (a *AuthMiddleware) validateJWT(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.secretKey, nil
	},
		jwt.WithIssuer(a.issuer),
		jwt.WithAudience(a.audience),
		jwt.WithLeeway(60*time.Second),
		jwt.WithValidMethods([]string{"HS256"}),
	)

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		if claims.UserID == "" || claims.Username == "" || claims.Role == "" {
			return nil, fmt.Errorf("token missing required claims")
		}
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// GenerateToken generates a JWT token for a user (utility function for testing/setup)
func (a *AuthMiddleware) GenerateToken(userID, username, role string, expirationHours int) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    a.issuer,
			Audience:  jwt.ClaimStrings{a.audience},
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
	if auth, ok := ctx.Value(authenticatedKey).(bool); ok && auth {
		return true
	}
	return false
}

// getAuthError returns any authentication error from the context
func getAuthError(ctx context.Context) string {
	if err, ok := ctx.Value(authErrorKey).(string); ok {
		return err
	}
	return ""
}

// getUserInfo extracts user information from the authenticated context
func getUserInfo(ctx context.Context) (userID, username, role string) {
	if val, ok := ctx.Value(userIDKey).(string); ok {
		userID = val
	}
	if val, ok := ctx.Value(usernameKey).(string); ok {
		username = val
	}
	if val, ok := ctx.Value(userRoleKey).(string); ok {
		role = val
	}
	return
}

// CreateTokenCommand creates a command-line utility to generate tokens
func CreateTokenCommand(secretKey, userID, username, role string, expirationHours int) {
	if secretKey == "" {
		log.Println("Error: TIME_AUTH_SECRET_KEY environment variable is required")
		return
	}

	auth, err := NewAuthMiddleware(secretKey, true, "TimeMCP", "TimeMCP-user")
	if err != nil {
		log.Printf("Error creating auth middleware: %v\n", err)
		return
	}

	token, err := auth.GenerateToken(userID, username, role, expirationHours)
	if err != nil {
		log.Printf("Error generating token: %v\n", err)
		return
	}

	log.Printf("Generated JWT token:\n%s\n\n", token)
	log.Printf("Token details:\n")
	log.Printf("  User ID: %s\n", userID)
	log.Printf("  Username: %s\n", username)
	log.Printf("  Role: %s\n", role)
	log.Printf("  Expires: %s\n", time.Now().Add(time.Duration(expirationHours)*time.Hour).Format(time.RFC3339))
	log.Printf("\nTo use this token, include it in HTTP requests:\n")
	log.Printf("  Authorization: Bearer %s\n", token)
}

func createHTTPMiddleware(config *Config) (server.HTTPContextFunc, error) {
	// Create authentication middleware
	var authMiddleware *AuthMiddleware
	if config.AuthEnabled {
		var err error
		authMiddleware, err = NewAuthMiddleware(config.AuthSecretKey, config.AuthEnabled, config.AuthIssuer, config.AuthAudience)
		if err != nil {
			return nil, fmt.Errorf("failed to create auth middleware: %v", err)
		}
		log.Println("HTTP authentication enabled")
	}

	return func(ctx context.Context, r *http.Request) context.Context {
		// Log HTTP request
		log.Printf("HTTP %s %s from %s\n", r.Method, r.URL.Path, r.RemoteAddr)

		// Apply authentication middleware if enabled
		if authMiddleware != nil {
			// Create a wrapper function for the next middleware step
			nextFunc := func(ctx context.Context, r *http.Request) context.Context {
				return ctx
			}
			// Apply authentication middleware
			ctx = authMiddleware.HTTPContextFunc(nextFunc)(ctx, r)
		}

		// Add request info to context
		ctx = context.WithValue(ctx, httpMethodKey, r.Method)
		ctx = context.WithValue(ctx, httpPathKey, r.URL.Path)
		ctx = context.WithValue(ctx, httpRemoteAddrKey, r.RemoteAddr)

		return ctx
	}, nil
}
func checkOrigin(originURL *url.URL, allowed string) bool {
	host := originURL.Host
	hostname := originURL.Hostname()

	if allowed == "*" {
		return true
	}

	if strings.HasPrefix(allowed, "*.") {
		domain := strings.TrimPrefix(allowed, "*.")
		return hostname == domain || strings.HasSuffix(hostname, "."+domain)
	}

	if strings.Contains(allowed, "://") {
		if u, err := url.Parse(allowed); err == nil {
			return u.Host == host || u.Hostname() == hostname
		}
		return false
	}

	if strings.Contains(allowed, ":") {
		return allowed == host
	}

	return allowed == hostname
}

func isOriginAllowed(origin string, allowedOrigins []string) bool {
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Host == "" {
		return false
	}

	for _, allowed := range allowedOrigins {
		if checkOrigin(originURL, allowed) {
			return true
		}
	}

	return false
}
