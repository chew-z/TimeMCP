package oauth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// The stored password format is four dot-separated fields:
//
//	pbkdf2-sha256.<iterations>.<hex salt>.<hex key>
//
// Dots and hex, deliberately: a crypt-style "$2y$12$..." string is safe in a
// systemd Environment= line but is mangled the moment a shell sources the same
// values without quoting them. This format has no character a shell, a .env
// parser or a Dockerfile treats specially, so one value works everywhere.
const (
	passwordScheme     = "pbkdf2-sha256"
	passwordIterations = 600_000 // OWASP 2023 guidance for PBKDF2-SHA256.
	passwordSaltLen    = 16
	passwordKeyLen     = 32
)

// HashPassword derives a new stored-password string from a plaintext password,
// using a freshly generated random salt.
func HashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating a salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyLen)
	if err != nil {
		return "", fmt.Errorf("deriving the key: %w", err)
	}
	return fmt.Sprintf("%s.%d.%s.%s", passwordScheme, passwordIterations,
		hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// parsedPassword is a stored-password string split into its four fields.
type parsedPassword struct {
	iterations int
	salt       []byte
	key        []byte
}

// parsePassword splits and validates a stored-password string. Every failure
// names what was wrong so a mangled value is diagnosable at startup rather
// than presenting as "the password is simply incorrect" at login.
func parsePassword(stored string) (parsedPassword, error) {
	var p parsedPassword
	fields := strings.Split(stored, ".")
	if len(fields) != 4 {
		return p, fmt.Errorf("expected 4 dot-separated fields, got %d", len(fields))
	}
	if fields[0] != passwordScheme {
		return p, fmt.Errorf("unknown scheme %q, want %q", fields[0], passwordScheme)
	}
	iterations, err := strconv.Atoi(fields[1])
	if err != nil || iterations < 1 {
		return p, fmt.Errorf("iteration count %q is not a positive integer", fields[1])
	}
	salt, err := hex.DecodeString(fields[2])
	if err != nil || len(salt) == 0 {
		return p, fmt.Errorf("the salt is not non-empty hex")
	}
	key, err := hex.DecodeString(fields[3])
	if err != nil || len(key) == 0 {
		return p, fmt.Errorf("the key is not non-empty hex")
	}
	p.iterations = iterations
	p.salt = salt
	p.key = key
	return p, nil
}

// ParsePasswordHash validates a password hash without exposing its contents.
func ParsePasswordHash(stored string) error {
	_, err := parsePassword(stored)
	return err
}

// verifyPassword reports whether password matches the stored-password string.
// It returns false rather than an error for a malformed stored value: the
// shape is checked once at startup, so reaching here with a bad one is not a
// condition a login handler can act on.
func verifyPassword(stored, password string) bool {
	p, err := parsePassword(stored)
	if err != nil {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, p.salt, p.iterations, len(p.key))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, p.key) == 1
}
