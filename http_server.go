package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

func createHttpServerOptions(config *Config) ([]server.StreamableHTTPOption, error) {
	var opts []server.StreamableHTTPOption

	if config.HTTPHeartbeat > 0 {
		opts = append(opts, server.WithHeartbeatInterval(config.HTTPHeartbeat))
	}

	if config.HTTPStateless {
		opts = append(opts, server.WithStateLess(true))
	}

	opts = append(opts, server.WithEndpointPath(config.HTTPPath))

	if config.HTTPCORSEnabled || config.AuthEnabled {
		httpContextFunc, err := createHTTPMiddleware(config)
		if err != nil {
			return nil, err
		}
		opts = append(opts, server.WithHTTPContextFunc(httpContextFunc))
	}

	return opts, nil
}

func createCustomHttpServer(httpServer http.Handler, config *Config) *http.Server {
	return &http.Server{
		Addr:         config.HTTPAddress,
		Handler:      createCustomHTTPHandler(httpServer, config),
		ReadTimeout:  config.HTTPTimeout,
		WriteTimeout: config.HTTPTimeout,
	}
}

func handleGracefulShutdown(server *http.Server, config *Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(1)
	errChan := make(chan error, 1)

	go func() {
		defer wg.Done()
		log.Printf("Starting TimeMCP HTTP server on %s\n", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server failed: %v\n", err)
			errChan <- err
			cancel()
		}
	}()

	select {
	case sig := <-sigChan:
		log.Printf("Received signal %v, shutting down HTTP server...\n", sig)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.HTTPTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v\n", err)
			return err
		}
		wg.Wait()
		log.Println("HTTP server stopped")
		return nil
	case err := <-errChan:
		wg.Wait()
		return err
	case <-ctx.Done():
		log.Println("Context cancelled, shutting down HTTP server...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.HTTPTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v\n", err)
			return err
		}
		wg.Wait()
		log.Println("HTTP server stopped")
		return nil
	}
}

// startHTTPServer starts the HTTP transport server
func startHTTPServer(mcpServer *server.MCPServer, config *Config) error {
	opts, err := createHttpServerOptions(config)
	if err != nil {
		return err
	}

	httpServer := server.NewStreamableHTTPServer(mcpServer, opts...)
	customServer := createCustomHttpServer(httpServer, config)

	return handleGracefulShutdown(customServer, config)
}

func createCustomHTTPHandler(mcpHandler http.Handler, config *Config) http.Handler {
	mux := http.NewServeMux()

	addHealthEndpoint(mux, config)
	addCapabilitiesEndpoint(mux, config)
	addCORSHandler(mux, mcpHandler, config)

	return mux
}

func addHealthEndpoint(mux *http.ServeMux, config *Config) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Health endpoint accessed from %s\n", r.RemoteAddr)

		health := map[string]any{
			"status":    "healthy",
			"service":   "TimeMCP",
			"version":   "1.0.0",
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

		if err := json.NewEncoder(w).Encode(health); err != nil {
			log.Printf("Failed to encode health response: %v\n", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})
}

func addCapabilitiesEndpoint(mux *http.ServeMux, config *Config) {
	mux.HandleFunc("/capabilities", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Capabilities endpoint accessed from %s\n", r.RemoteAddr)

		capabilities := getCapabilities()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")

		if config.HTTPCORSEnabled {
			origin := r.Header.Get("Origin")
			if origin != "" && isOriginAllowed(origin, config.HTTPCORSOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
		}

		if err := json.NewEncoder(w).Encode(capabilities); err != nil {
			log.Printf("Failed to encode capabilities response: %v\n", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})
}

func getCapabilities() map[string]any {
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "get_current_time",
				"description": "Get current time in a specific timezone or system timezone.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"timezone": map[string]any{
							"type":        "string",
							"description": "The timezone to get the current time in. If not provided, system timezone is used.",
						},
					},
				},
			},
			{
				"name":        "convert_time",
				"description": "Convert time between timezones.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"source_timezone": map[string]any{
							"type":        "string",
							"description": "Source timezone. Defaults to system timezone if not provided.",
						},
						"time": map[string]any{
							"type":        "string",
							"description": "Time in 24-hour format (HH:MM). Defaults to current time if not provided.",
						},
						"target_timezone": map[string]any{
							"type":        "string",
							"description": "Target timezone to convert the time to.",
							"required":    true,
						},
					},
					"required": []string{"target_timezone"},
				},
			},
		},
	}
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

			if r.Method == "OPTIONS" {
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		mcpHandler.ServeHTTP(w, r)
	}
	mux.HandleFunc("/", handler)
}
