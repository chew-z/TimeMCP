package main

import "testing"

func TestIsOriginAllowedExactHost(t *testing.T) {
	allowed := []string{"example.com"}
	if !isOriginAllowed("https://example.com", allowed) {
		t.Errorf("expected exact host to be allowed")
	}
	if isOriginAllowed("https://evil.com", allowed) {
		t.Errorf("did not expect different host to be allowed")
	}
}

func TestIsOriginAllowedWildcard(t *testing.T) {
	allowed := []string{"*.example.com"}
	if !isOriginAllowed("https://sub.example.com", allowed) {
		t.Errorf("expected subdomain allowed")
	}
	if !isOriginAllowed("https://example.com", allowed) {
		t.Errorf("expected apex domain allowed with wildcard")
	}
	if isOriginAllowed("https://sub.evil.com", allowed) {
		t.Errorf("did not expect other domain allowed")
	}
}

func TestIsOriginAllowedPort(t *testing.T) {
	allowed := []string{"localhost:3000", "127.0.0.1:8080"}
	if !isOriginAllowed("http://localhost:3000", allowed) {
		t.Errorf("expected localhost:3000 allowed")
	}
	if !isOriginAllowed("http://127.0.0.1:8080", allowed) {
		t.Errorf("expected 127.0.0.1:8080 allowed")
	}
	if isOriginAllowed("http://localhost:4000", allowed) {
		t.Errorf("did not expect localhost:4000 allowed")
	}
}
