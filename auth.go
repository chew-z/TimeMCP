package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"TimeMCP/internal/oauth"

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
	oauthCfg  *oauth.Config
}

// Claims represents JWT token claims
type Claims struct {
	jwt.RegisteredClaims
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Scope    string `json:"scope,omitempty"`
}

const oauthRole = "oauth"

// HS256Signer signs OAuth access tokens with the existing authentication claims.
type HS256Signer struct{ Secret, Issuer, Audience string }

var _ oauth.Signer = HS256Signer{}

// Sign mints an HS256 OAuth token carrying the claims required by TimeMCP authentication.
func (s HS256Signer) Sign(username, scope string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("signing OAuth access token: TTL must be positive")
	}
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.Issuer,
			Audience:  jwt.ClaimStrings{s.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		UserID: username, Username: username, Role: oauthRole, Scope: scope,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.Secret))
	if err != nil {
		return "", fmt.Errorf("signing OAuth access token: %w", err)
	}
	return token, nil
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(secretKey string, enabled bool, issuer string, audience string, oauthCfg *oauth.Config) (*AuthMiddleware, error) {
	if enabled && secretKey == "" {
		return nil, fmt.Errorf("auth enabled but secret key is empty")
	}
	return &AuthMiddleware{
		secretKey: []byte(secretKey),
		enabled:   enabled,
		issuer:    issuer,
		audience:  audience,
		oauthCfg:  oauthCfg,
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
			slog.Warn("missing or invalid authorization header", "remote_addr", r.RemoteAddr)
			// Set authentication error in context instead of failing the request
			ctx = context.WithValue(ctx, authErrorKey, authErrorMissingToken)
			ctx = context.WithValue(ctx, authenticatedKey, false)
			return next(ctx, r)
		}

		token := parts[1]

		// Validate JWT token
		claims, err := a.validateJWT(token)
		if err != nil {
			slog.Warn("invalid token", "remote_addr", r.RemoteAddr, "error", err)
			errorKey := authErrorInvalidToken
			if errors.Is(err, jwt.ErrTokenExpired) {
				errorKey = authErrorExpiredToken
			}
			ctx = context.WithValue(ctx, authErrorKey, errorKey)
			ctx = context.WithValue(ctx, authenticatedKey, false)
			return next(ctx, r)
		}

		// Add user to request context
		ctx = context.WithValue(ctx, authenticatedKey, true)
		ctx = context.WithValue(ctx, userIDKey, claims.UserID)
		ctx = context.WithValue(ctx, usernameKey, claims.Username)
		ctx = context.WithValue(ctx, userRoleKey, claims.Role)

		return next(ctx, r)
	}
}

// errInsufficientScope identifies a valid OAuth token that lacks the configured scope.
var errInsufficientScope = errors.New("insufficient scope")

// validateJWT first applies the unchanged static profile, then the optional OAuth
// profile. OAuth tokens additionally require expiration and an exact scope match.
func (a *AuthMiddleware) validateJWT(tokenString string) (*Claims, error) {
	claims, err := a.validateJWTProfile(tokenString, a.issuer, a.audience, false)
	if err == nil || a.oauthCfg == nil {
		return claims, err
	}
	claims, err = a.validateJWTProfile(tokenString, a.oauthCfg.Issuer, a.oauthCfg.Resource, true)
	if err != nil {
		return nil, err
	}
	if claims.Scope != a.oauthCfg.Scope {
		return nil, errInsufficientScope
	}
	return claims, nil
}

// validateJWTProfile verifies the signature, registered claims, and identity for one profile.
func (a *AuthMiddleware) validateJWTProfile(tokenString, issuer, audience string, requireExpiration bool) (*Claims, error) {
	opts := []jwt.ParserOption{
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithLeeway(60 * time.Second),
		jwt.WithValidMethods([]string{"HS256"}),
	}
	if requireExpiration {
		opts = append(opts, jwt.WithExpirationRequired())
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.secretKey, nil
	}, opts...)

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

// protectMCP sends OAuth discovery challenges before mcp-go handles requests.
// It is installed only with OAuth configured; OPTIONS bypasses authentication.
func (a *AuthMiddleware) protectMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		var err error
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			err = errors.New("missing or malformed bearer authorization")
		} else {
			_, err = a.validateJWT(parts[1])
		}
		if err == nil {
			next.ServeHTTP(w, r)
			return
		}
		status, challengeError, bodyError := http.StatusUnauthorized, "invalid_token", "unauthorized"
		if errors.Is(err, errInsufficientScope) {
			status, challengeError, bodyError = http.StatusForbidden, "insufficient_scope", "insufficient_scope"
		}
		slog.Debug("MCP authentication rejected", "remote_addr", r.RemoteAddr, "error", err)
		a.writeChallenge(w, challengeError)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": bodyError}); err != nil {
			slog.Warn("writing authentication error response", "error", err)
		}
	})
}

// writeChallenge advertises the OAuth protected-resource metadata and required scope.
func (a *AuthMiddleware) writeChallenge(w http.ResponseWriter, code string) {
	metadataURL := oauth.IssuerOrigin(a.oauthCfg.Issuer) + oauth.ProtectedResourceMetadataPath(a.oauthCfg.Resource)
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(
		"Bearer realm=%q, error=%q, resource_metadata=%q, scope=%q",
		a.oauthCfg.Realm, code, metadataURL, a.oauthCfg.Scope,
	))
}

// GenerateToken generates a JWT token for a user (utility function for testing/setup)
func (a *AuthMiddleware) GenerateToken(userID, username, role string, expirationHours int) (string, error) {
	now := time.Now()
	claims := Claims{
		Issuer:    a.issuer,
		Audience:  jwt.ClaimStrings{a.audience},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expirationHours) * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		UserID:    userID,
		Username:  username,
		Role:      role,
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
func CreateTokenCommand(secretKey, userID, username, role string, expirationHours int) error {
	if secretKey == "" {
		return errors.New("TIME_AUTH_SECRET_KEY environment variable is required")
	}

	auth, err := NewAuthMiddleware(secretKey, true, "TimeMCP", "TimeMCP-user", nil)
	if err != nil {
		return fmt.Errorf("creating auth middleware: %w", err)
	}

	token, err := auth.GenerateToken(userID, username, role, expirationHours)
	if err != nil {
		return fmt.Errorf("generating token: %w", err)
	}

	fmt.Printf("Generated JWT token:\n%s\n\n", token)
	fmt.Printf("Token details:\n")
	fmt.Printf("  User ID: %s\n", userID)
	fmt.Printf("  Username: %s\n", username)
	fmt.Printf("  Role: %s\n", role)
	fmt.Printf("  Expires: %s\n", time.Now().Add(time.Duration(expirationHours)*time.Hour).Format(time.RFC3339))
	fmt.Printf("\nTo use this token, include it in HTTP requests:\n")
	fmt.Printf("  Authorization: Bearer %s\n", token)
	return nil
}

func createHTTPMiddleware(config *Config, oauthCfg *oauth.Config) (server.HTTPContextFunc, error) {
	// Create authentication middleware
	var authMiddleware *AuthMiddleware
	if config.AuthEnabled {
		var err error
		authMiddleware, err = NewAuthMiddleware(config.AuthSecretKey, config.AuthEnabled, config.AuthIssuer, config.AuthAudience, oauthCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create auth middleware: %w", err)
		}
		slog.Info("HTTP authentication enabled")
	}

	return func(ctx context.Context, r *http.Request) context.Context {

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

	if after, ok := strings.CutPrefix(allowed, "*."); ok {
		domain := after
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
