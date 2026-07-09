package main

import (
	"reflect"
	"testing"
)

func TestCORSDefaults(t *testing.T) {
	t.Setenv("TIME_HTTP_CORS_ENABLED", "")
	t.Setenv("TIME_HTTP_CORS_ORIGINS", "")
	t.Setenv("TIME_AUTH_ENABLED", "")

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig error: %v", err)
	}
	if cfg.HTTPCORSEnabled {
		t.Errorf("expected HTTPCORSEnabled=false by default")
	}
	if len(cfg.HTTPCORSOrigins) != 0 {
		t.Errorf("expected no default CORS origins; got %v", cfg.HTTPCORSOrigins)
	}
}

func TestCORSParseNormalization(t *testing.T) {
	t.Setenv("TIME_HTTP_CORS_ENABLED", "true")
	t.Setenv("TIME_AUTH_ENABLED", "false")
	t.Setenv("TIME_HTTP_CORS_ORIGINS", "https://example.com, sub.example.com, *.example.org, localhost:3000, http://127.0.0.1:8080")

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig error: %v", err)
	}

	expected := map[string]struct{}{
		"example.com":     {},
		"sub.example.com": {},
		"*.example.org":   {},
		"localhost:3000":  {},
		"127.0.0.1:8080":  {},
	}

	got := map[string]struct{}{}
	for _, v := range cfg.HTTPCORSOrigins {
		got[v] = struct{}{}
	}

	if !reflect.DeepEqual(expected, got) {
		t.Errorf("expected origins %v, got %v", expected, got)
	}
}

func TestCORSStarWithAuthEnabled(t *testing.T) {
	t.Setenv("TIME_HTTP_CORS_ENABLED", "true")
	t.Setenv("TIME_HTTP_CORS_ORIGINS", "*")
	t.Setenv("TIME_AUTH_ENABLED", "true")
	t.Setenv("TIME_AUTH_SECRET_KEY", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")

	if _, err := NewConfig(); err == nil {
		t.Fatalf("expected error when CORS origins includes * with auth enabled")
	}
}
