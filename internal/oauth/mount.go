package oauth

import (
	"log/slog"
	"net/http"
)

// Mount registers OAuth discovery and endpoint handlers on mux.
func Mount(mux *http.ServeMux, cfg Config, store *Store, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	registry := NewClientRegistry(store, logger)
	authorization := NewAuthorizationService(cfg, store, registry, logger)
	tokens := NewTokenService(cfg, store, logger)

	protectedPath := ProtectedResourceMetadataPath(cfg.Resource)
	mux.HandleFunc(protectedPath, handleProtectedResourceMetadata(cfg))
	if protectedPath != "/.well-known/oauth-protected-resource" {
		mux.HandleFunc("/.well-known/oauth-protected-resource", handleProtectedResourceMetadata(cfg))
	}
	authorizationPath := authorizationServerMetadataPath(cfg.Issuer)
	mux.HandleFunc(authorizationPath, handleAuthorizationServerMetadata(cfg))
	if authorizationPath != "/.well-known/oauth-authorization-server" {
		mux.HandleFunc("/.well-known/oauth-authorization-server", handleAuthorizationServerMetadata(cfg))
	}
	mux.HandleFunc("/authorize", authorization.AuthorizeHandler())
	mux.HandleFunc("/login", authorization.LoginHandler())
	mux.HandleFunc("/token", tokens.TokenHandler())
	mux.HandleFunc("/register", registry.RegisterHandler())
}
