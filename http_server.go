package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
)

// startHTTPServer starts the HTTP transport server
func startHTTPServer(mcpServer *server.MCPServer, config *Config) error {
	// Create HTTP server options
	var opts []server.StreamableHTTPOption

	// Configure heartbeat if enabled
	if config.HTTPHeartbeat > 0 {
		opts = append(opts, server.WithHeartbeatInterval(config.HTTPHeartbeat))
	}

	// Configure stateless mode
	if config.HTTPStateless {
		opts = append(opts, server.WithStateLess(true))
	}

	// Configure endpoint path
	opts = append(opts, server.WithEndpointPath(config.HTTPPath))

	// Add HTTP context function for CORS, logging, and authentication
	if config.HTTPCORSEnabled || config.AuthEnabled {
		opts = append(opts, server.WithHTTPContextFunc(createHTTPMiddleware(config)))
	}

	// Create streamable HTTP server
	httpServer := server.NewStreamableHTTPServer(mcpServer, opts...)

	// Create custom HTTP server with health endpoint
	customServer := &http.Server{
		Addr:         config.HTTPAddress,
		Handler:      createCustomHTTPHandler(httpServer, config),
		ReadTimeout:  config.HTTPTimeout,
		WriteTimeout: config.HTTPTimeout,
	}

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(1)

	// Start server in goroutine
	go func() {
		defer wg.Done()
		fmt.Printf("Starting TimeMCP HTTP server on %s%s\n", config.HTTPAddress, config.HTTPPath)
		if err := customServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("HTTP server failed to start: %v\n", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	select {
	case sig := <-sigChan:
		fmt.Printf("Received signal %v, shutting down HTTP server...\n", sig)
	case <-ctx.Done():
		fmt.Println("Context cancelled, shutting down HTTP server...")
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.HTTPTimeout)
	defer shutdownCancel()

	if err := customServer.Shutdown(shutdownCtx); err != nil {
		fmt.Printf("HTTP server shutdown error: %v\n", err)
		return err
	}

	wg.Wait()
	fmt.Println("HTTP server stopped")
	return nil
}

// createCustomHTTPHandler creates a custom HTTP handler that includes health endpoint
func createCustomHTTPHandler(mcpHandler http.Handler, config *Config) http.Handler {
	mux := http.NewServeMux()

	// Add health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Health endpoint accessed from %s\n", r.RemoteAddr)

		// Create health response
		health := map[string]interface{}{
			"status":    "healthy",
			"service":   "TimeMCP",
			"version":   "1.0.0",
			"timestamp": fmt.Sprintf("%d", r.Context().Value("timestamp")),
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		// Add CORS headers if enabled
		if config.HTTPCORSEnabled {
			origin := r.Header.Get("Origin")
			if origin != "" && isOriginAllowed(origin, config.HTTPCORSOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
		}

		if err := json.NewEncoder(w).Encode(health); err != nil {
			fmt.Printf("Failed to encode health response: %v\n", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})

	// Add capabilities endpoint
	mux.HandleFunc("/capabilities", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Capabilities endpoint accessed from %s\n", r.RemoteAddr)

		capabilities := map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "get_current_time",
					"description": "Get current time in a specific timezone or system timezone.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"timezone": map[string]interface{}{
								"type":        "string",
								"description": "The timezone to get the current time in. If not provided, system timezone is used.",
							},
						},
					},
				},
				{
					"name":        "convert_time",
					"description": "Convert time between timezones.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"source_timezone": map[string]interface{}{
								"type":        "string",
								"description": "Source timezone. Defaults to system timezone if not provided.",
							},
							"time": map[string]interface{}{
								"type":        "string",
								"description": "Time in 24-hour format (HH:MM). Defaults to current time if not provided.",
							},
							"target_timezone": map[string]interface{}{
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

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")

		// Add CORS headers if enabled
		if config.HTTPCORSEnabled {
			origin := r.Header.Get("Origin")
			if origin != "" && isOriginAllowed(origin, config.HTTPCORSOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
		}

		if err := json.NewEncoder(w).Encode(capabilities); err != nil {
			fmt.Printf("Failed to encode capabilities response: %v\n", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})

	// Handle CORS preflight requests
	if config.HTTPCORSEnabled {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "OPTIONS" {
				origin := r.Header.Get("Origin")
				if origin != "" && isOriginAllowed(origin, config.HTTPCORSOrigins) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
					w.Header().Set("Access-Control-Max-Age", "86400")
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			// Pass to MCP handler for all other requests
			mcpHandler.ServeHTTP(w, r)
		})
	} else {
		// Handle all other requests with the MCP handler
		mux.Handle("/", mcpHandler)
	}

	return mux
}