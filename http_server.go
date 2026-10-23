package main

import (
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

const readHeaderTimeout = 10 * time.Second

func createHTTPServerOptions(config *Config) ([]server.StreamableHTTPOption, error) {
	var opts []server.StreamableHTTPOption

	if config.HTTPHeartbeat > 0 {
		opts = append(opts, server.WithHeartbeatInterval(config.HTTPHeartbeat))
	}

	if config.HTTPStateless {
		opts = append(opts, server.WithStateLess(true))
	}

	opts = append(opts, server.WithEndpointPath(config.HTTPPath))

	if config.HTTPDisableLocalhostProtection {
		opts = append(opts, server.WithDisableLocalhostProtection(true))
	}

	if config.HTTPSessionIdleTTL > 0 {
		opts = append(opts, server.WithSessionIdleTTL(config.HTTPSessionIdleTTL))
	}

	if config.HTTPCORSEnabled || config.AuthEnabled {
		httpContextFunc, err := createHTTPMiddleware(config)
		if err != nil {
			return nil, err
		}
		opts = append(opts, server.WithHTTPContextFunc(httpContextFunc))
	}

	return opts, nil
}

func createCustomHTTPServer(httpServer http.Handler, config *Config) *http.Server {
	return &http.Server{
		Addr:              config.HTTPAddress,
		Handler:           createCustomHTTPHandler(httpServer, config),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       config.HTTPTimeout,
		WriteTimeout:      config.HTTPTimeout,
	}
}

func handleGracefulShutdown(server *http.Server, config *Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	serveErrCh := make(chan error, 1)

	wg.Go(func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
		}
	})

	select {
	case <-ctx.Done():
		slog.Info("received signal, shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.HTTPTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down HTTP server: %w", err)
		}
		wg.Wait()
		slog.Info("HTTP server stopped")
		return nil
	case err := <-serveErrCh:
		wg.Wait()
		return fmt.Errorf("serving HTTP: %w", err)
	}
}

// startHTTPServer starts the HTTP transport server
func startHTTPServer(mcpServer *server.MCPServer, config *Config) error {
	opts, err := createHTTPServerOptions(config)
	if err != nil {
		return err
	}

	httpServer := server.NewStreamableHTTPServer(mcpServer, opts...)
	customServer := createCustomHTTPServer(httpServer, config)

	return handleGracefulShutdown(customServer, config)
}

func createCustomHTTPHandler(mcpHandler http.Handler, config *Config) http.Handler {
	mux := http.NewServeMux()

	addHealthEndpoint(mux, config)
	addCORSHandler(mux, mcpHandler, config)

	return mux
}

func addHealthEndpoint(mux *http.ServeMux, config *Config) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		health := map[string]any{
			"status":    "healthy",
			"service":   "TimeMCP",
			"version":   serverVersion,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		if config.HTTPCORSEnabled {
			origin := r.Header.Get("Origin")
			if origin != "" && isOriginAllowed(origin, config.HTTPCORSOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
		}

		if err := jsonv2.MarshalWrite(w, health); err != nil {
			slog.Error("failed to encode health response", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})
}

func addCORSHandler(mux *http.ServeMux, mcpHandler http.Handler, config *Config) {
	if !config.HTTPCORSEnabled {
		mux.Handle("/", mcpHandler)
		return
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isOriginAllowed(origin, config.HTTPCORSOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		mcpHandler.ServeHTTP(w, r)
	}
	mux.HandleFunc("/", handler)
}
