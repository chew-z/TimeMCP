package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthMiddleware(t *testing.T) {
	secret := "test-secret"
	auth := NewAuthMiddleware(secret, true)
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(auth.HTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
		return ctx
	})(nextHandler))
	defer server.Close()

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

	testCases := []struct {
		name          string
		authHeader    string
		expectedStatus int
		expectedAuth  bool
		expectedError string
	}{
		{
			name:          "Valid Token",
			authHeader:    "Bearer " + validToken,
			expectedStatus: http.StatusOK,
			expectedAuth:  true,
		},
		{
			name:          "Invalid Token - Wrong Secret",
			authHeader:    "Bearer " + validToken, // Use a different middleware to validate
			expectedStatus: http.StatusOK,
			expectedAuth:  false,
			expectedError: "invalid_token",
		},
		{
			name:          "Expired Token",
			authHeader:    "Bearer " + expiredToken,
			expectedStatus: http.StatusOK,
			expectedAuth:  false,
			expectedError: "expired_token",
		},
		{
			name:          "Missing Token",
			authHeader:    "",
			expectedStatus: http.StatusOK,
			expectedAuth:  false,
			expectedError: "missing_token",
		},
		{
			name:          "Malformed Bearer",
			authHeader:    "Bearer" + validToken, // No space
			expectedStatus: http.StatusOK,
			expectedAuth:  false,
			expectedError: "missing_token",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", server.URL, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			// Create a context for the handler
			ctx := context.Background()

			// Create a custom handler to check the context values
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// For the wrong secret case, use a different auth middleware
				if tc.name == "Invalid Token - Wrong Secret" {
					auth = NewAuthMiddleware("wrong-secret", true)
				}

				ctx = auth.HTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
					return ctx
				})(ctx, r)

				authenticated := isAuthenticated(ctx)
				authError := getAuthError(ctx)

				if authenticated != tc.expectedAuth {
					t.Errorf("Expected authenticated to be %v, but got %v", tc.expectedAuth, authenticated)
				}

				if authError != tc.expectedError {
					t.Errorf("Expected auth error to be '%s', but got '%s'", tc.expectedError, authError)
				}
			})

			handler.ServeHTTP(httptest.NewRecorder(), req)
		})
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

	if err != jwt.ErrSignatureInvalid {
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

	if err != jwt.ErrTokenExpired {
		t.Errorf("Expected error to be %v, but got %v", jwt.ErrTokenExpired, err)
	}
}
