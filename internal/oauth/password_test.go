package oauth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const password = "correct horse battery staple"
	stored, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !verifyPassword(stored, password) {
		t.Errorf("verifyPassword() = false for the password it was derived from")
	}
	if verifyPassword(stored, password+"x") {
		t.Errorf("verifyPassword() = true for a wrong password")
	}
}

// TestHashPasswordShellSafe guards the reason this format exists: the value
// must survive a systemd unit, an env file and a shell that sources one
// without quoting, so it may contain no character any of them treats
// specially.
func TestHashPasswordShellSafe(t *testing.T) {
	stored, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	const special = "$`\\\"'!*?~#&|;<>() \t\n{}[]%"
	if i := strings.IndexAny(stored, special); i >= 0 {
		t.Errorf("stored password contains %q at index %d, which a shell, an env "+
			"parser or systemd would treat specially: %q", stored[i], i, stored)
	}
}

func TestHashPasswordSaltsEachCall(t *testing.T) {
	first, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	second, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if first == second {
		t.Errorf("two hashes of the same password are identical; the salt is not random")
	}
}

func TestParsePassword(t *testing.T) {
	valid, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	tests := []struct {
		name    string
		stored  string
		wantErr string
	}{
		{name: "valid", stored: valid},
		{name: "empty", stored: "", wantErr: "4 dot-separated fields"},
		{name: "too few fields", stored: "pbkdf2-sha256.600000.aabb", wantErr: "4 dot-separated fields"},
		{name: "too many fields", stored: valid + ".extra", wantErr: "4 dot-separated fields"},
		{name: "unknown scheme", stored: "bcrypt.600000.aabb.ccdd", wantErr: "unknown scheme"},
		{name: "iterations not a number", stored: "pbkdf2-sha256.many.aabb.ccdd", wantErr: "positive integer"},
		{name: "iterations zero", stored: "pbkdf2-sha256.0.aabb.ccdd", wantErr: "positive integer"},
		{name: "salt not hex", stored: "pbkdf2-sha256.600000.zzzz.ccdd", wantErr: "salt"},
		{name: "salt empty", stored: "pbkdf2-sha256.600000..ccdd", wantErr: "salt"},
		{name: "key not hex", stored: "pbkdf2-sha256.600000.aabb.zzzz", wantErr: "key"},
		{name: "key empty", stored: "pbkdf2-sha256.600000.aabb.", wantErr: "key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePassword(test.stored)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("parsePassword() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("parsePassword() error = nil, want one mentioning %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("parsePassword() error = %q, want one mentioning %q", err, test.wantErr)
			}
		})
	}
}

// TestVerifyPasswordRejectsMalformed covers the login path reached with a
// stored value that never passed the startup check.
func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	for _, stored := range []string{"", "not-a-hash", "$2y$12$abcdefghijklmnopqrstuv"} {
		if verifyPassword(stored, "secret") {
			t.Errorf("verifyPassword(%q, ...) = true, want false", stored)
		}
	}
}

func TestParsePasswordHashRejectsMalformed(t *testing.T) {
	if err := ParsePasswordHash("not-a-password-hash"); err == nil {
		t.Fatal("ParsePasswordHash() error = nil for malformed input")
	}
}
