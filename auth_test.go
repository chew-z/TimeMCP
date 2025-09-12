package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthMiddleware(t *testing.T) {
	secret := "test-secret"
	auth := NewAuthMiddleware(secret, true)

	// Generate a valid token
	validToken, err := auth.GenerateToken("1", "testuser", "user", 1)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// Generate an expired token
	expiredToken, err := auth.GenerateToken("1", "testuser", "user", -1)
	if err != nil {
		t.Fatalf("Failed to generate expired token: %v", err)
	}

	// Generate a token with a different secret
	auth2 := NewAuthMiddleware("different-secret", true)
	invalidSecretToken, err := auth2.GenerateToken("1", "testuser", "user", 1)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	testCases := []struct {
		name          string
		authHeader    string
		expectedAuth  bool
		expectedError string
		expectedUserID string
	}{
		{
			name:          "Valid Token",
			authHeader:    "Bearer " + validToken,
			expectedAuth:  true,
			expectedUserID: "1",
		},
		{
			name:          "Invalid Token - Wrong Secret",
			authHeader:    "Bearer " + invalidSecretToken,
			expectedAuth:  false,
			expectedError: "invalid_token",
		},
		{
			name:          "Expired Token",
			authHeader:    "Bearer " + expiredToken,
			expectedAuth:  false,
			expectedError: "expired_token",
		},
		{
			name:          "Missing Token",
			authHeader:    "",
			expectedAuth:  false,
			expectedError: "missing_token",
		},
		{
			name:          "Malformed Bearer",
			authHeader:    "Bearer" + validToken, // No space
			expectedAuth:  false,
			expectedError: "missing_token",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			// The next function in the middleware chain.
			next := func(ctx context.Context, r *http.Request) context.Context {
				return ctx
			}

			// Apply the middleware.
			ctx := auth.HTTPContextFunc(next)(context.Background(), req)

			// Check the context values.
			authenticated := isAuthenticated(ctx)
			authError := getAuthError(ctx)
			userID, _, _ := getUserInfo(ctx)

			if authenticated != tc.expectedAuth {
				t.Errorf("Expected authenticated to be %v, but got %v", tc.expectedAuth, authenticated)
			}

			if authError != tc.expectedError {
				t.Errorf("Expected auth error to be '%s', but got '%s'", tc.expectedError, authError)
			}

			if authenticated && userID != tc.expectedUserID {
				t.Errorf("Expected userID to be '%s', but got '%s'", tc.expectedUserID, userID)
			}
		})
	}
}

func TestAuthMiddleware_Disabled(t *testing.T) {
	secret := "test-secret"
	// Auth middleware is disabled
	auth := NewAuthMiddleware(secret, false)

	req, _ := http.NewRequest("GET", "/", nil)
	// Even with a valid token, auth should be skipped
	req.Header.Set("Authorization", "Bearer some-token")

	next := func(ctx context.Context, r *http.Request) context.Context {
		return ctx
	}

	ctx := auth.HTTPContextFunc(next)(context.Background(), req)

	// Check that no auth values are set in the context
	if isAuthenticated(ctx) {
		t.Error("Expected authenticated to be false when auth is disabled")
	}
	if getAuthError(ctx) != "" {
		t.Error("Expected auth error to be empty when auth is disabled")
	}
}


func TestGenerateAndValidateToken(t *testing.T) {
	secret := "test-secret"
	auth := NewAuthMiddleware(secret, true)

	userID := "123"
	username := "testuser"
	role := "admin"

	token, err := auth.GenerateToken(userID, username, role, 1)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	claims, err := auth.validateJWT(token)
	if err != nil {
		t.Fatalf("Failed to validate token: %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("Expected UserID to be %s, but got %s", userID, claims.UserID)
	}

	if claims.Username != username {
		t.Errorf("Expected Username to be %s, but got %s", username, claims.Username)
	}

	if claims.Role != role {
		t.Errorf("Expected Role to be %s, but got %s", role, claims.Role)
	}
}

func TestValidateJWT_InvalidSignature(t *testing.T) {
	auth1 := NewAuthMiddleware("secret1", true)
	auth2 := NewAuthMiddleware("secret2", true)

	token, err := auth1.GenerateToken("1", "user", "user", 1)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	_, err = auth2.validateJWT(token)
	if err == nil {
		t.Fatal("Expected an error for invalid signature, but got nil")
	}

	if !errors.Is(err, jwt.ErrSignatureInvalid) {
		t.Errorf("Expected error to be %v, but got %v", jwt.ErrSignatureInvalid, err)
	}
}

func TestValidateJWT_ExpiredToken(t *testing.T) {
	auth := NewAuthMiddleware("secret", true)

	token, err := auth.GenerateToken("1", "user", "user", -1) // Expires in the past
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	_, err = auth.validateJWT(token)
	if err == nil {
		t.Fatal("Expected an error for expired token, but got nil")
	}

	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("Expected error to be %v, but got %v", jwt.ErrTokenExpired, err)
	}
}
