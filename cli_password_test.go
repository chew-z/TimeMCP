package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"TimeMCP/internal/oauth"
)

func TestRunHashPassword(t *testing.T) {
	for _, input := range []string{"pw\n", "pw\r\n", "pw"} {
		t.Run(input, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := runHashPassword(strings.NewReader(input), &stdout, &stderr); err != nil {
				t.Fatalf("runHashPassword: %v", err)
			}
			hash := strings.TrimSpace(stdout.String())
			if err := oauth.ParsePasswordHash(hash); err != nil {
				t.Fatalf("invalid password hash: %v", err)
			}
			if !strings.HasSuffix(stdout.String(), "\n") || strings.Count(stdout.String(), "\n") != 1 {
				t.Fatal("stdout must contain one hash line")
			}
			if !strings.Contains(stderr.String(), "Password:") {
				t.Fatalf("prompt missing: %q", stderr.String())
			}
		})
	}
}

func TestRunHashPasswordEmpty(t *testing.T) {
	for _, input := range []string{"", "\n", "\r\n"} {
		var stdout, stderr bytes.Buffer
		if err := runHashPassword(strings.NewReader(input), &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("input %q: expected empty-password error, got %v", input, err)
		}
		if stdout.Len() != 0 {
			t.Fatal("empty password produced output")
		}
	}
}

func TestRunHashPasswordReadError(t *testing.T) {
	readErr := errors.New("test read failure")
	var stdout, stderr bytes.Buffer
	if err := runHashPassword(iotest.ErrReader(readErr), &stdout, &stderr); !errors.Is(err, readErr) {
		t.Fatalf("expected reader error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatal("failed read produced output")
	}
}

// passwordErrorWriter simulates a failed output stream.
type passwordErrorWriter struct{ err error }

func (w passwordErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestRunHashPasswordWriteError(t *testing.T) {
	writeErr := errors.New("test write failure")
	for _, tc := range []struct {
		name           string
		stdout, stderr io.Writer
	}{
		{"prompt", io.Discard, passwordErrorWriter{writeErr}},
		{"hash", passwordErrorWriter{writeErr}, io.Discard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runHashPassword(strings.NewReader("pw\n"), tc.stdout, tc.stderr); !errors.Is(err, writeErr) {
				t.Fatalf("expected writer error, got %v", err)
			}
		})
	}
}
