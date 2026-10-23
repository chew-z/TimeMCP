package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"TimeMCP/internal/oauth"

	"golang.org/x/term"
)

// readPassword reads one line, suppressing echo when stdin is a terminal.
// The variable provides a test seam for terminal input.
var readPassword = func(stdin io.Reader, stderr io.Writer) (string, error) {
	if file, ok := stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		value, err := term.ReadPassword(int(file.Fd()))
		if err != nil {
			return "", fmt.Errorf("reading terminal password: %w", err)
		}
		if _, err := fmt.Fprintln(stderr); err != nil {
			return "", fmt.Errorf("writing password prompt newline: %w", err)
		}
		return string(value), nil
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// runHashPassword prints a PBKDF2 hash for TIME_OAUTH_LOGIN_PASSWORD_HASH.
// The prompt is written to stderr so stdout contains only the hash.
func runHashPassword(stdin io.Reader, stdout, stderr io.Writer) error {
	if _, err := fmt.Fprint(stderr, "Password: "); err != nil {
		return fmt.Errorf("writing password prompt: %w", err)
	}
	password, err := readPassword(stdin, stderr)
	if err != nil {
		return fmt.Errorf("reading hash-password input: %w", err)
	}
	if password == "" {
		return errors.New("password must not be empty")
	}
	hash, err := oauth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, hash); err != nil {
		return fmt.Errorf("writing password hash: %w", err)
	}
	return nil
}
