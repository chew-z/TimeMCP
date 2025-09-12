package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/araddon/dateparse"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// loadTimezone loads a timezone location, using config default or system timezone if empty
func loadTimezone(tzStr string, config *Config) (*time.Location, error) {
	if tzStr == "" {
		// Use default timezone from config if available
		if config.DefaultTimezone != "" {
			return time.LoadLocation(config.DefaultTimezone)
		}
		// Fall back to system timezone
		return time.Local, nil
	}
	return time.LoadLocation(tzStr)
}

func main() {
	// Define command-line flags for configuration override
	transportFlag := flag.String("transport", "stdio", "Transport mode: 'stdio' (default) or 'http'")

	// Authentication flags
	authEnabledFlag := flag.Bool("auth-enabled", false, "Enable JWT authentication for HTTP transport")
	generateTokenFlag := flag.Bool("generate-token", false, "Generate a JWT token and exit")
	tokenUserIDFlag := flag.String("token-user-id", "user1", "User ID for token generation")
	tokenUsernameFlag := flag.String("token-username", "admin", "Username for token generation")
	tokenRoleFlag := flag.String("token-role", "admin", "Role for token generation")
	tokenExpirationFlag := flag.Int("token-expiration", 744, "Token expiration in hours (default: 744 = 31 days)")

	flag.Parse()

	// Handle token generation if requested
	if *generateTokenFlag {
		secretKey := os.Getenv("TIME_AUTH_SECRET_KEY")
		CreateTokenCommand(secretKey, *tokenUserIDFlag, *tokenUsernameFlag, *tokenRoleFlag, *tokenExpirationFlag)
		return
	}

	// Create configuration from environment variables
	config, err := NewConfig()
	if err != nil {
		log.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Override authentication if flag is provided
	if *authEnabledFlag {
		config.AuthEnabled = true
		log.Println("Authentication feature enabled via command line flag")
	}

	// Create a new MCP server
	mcpServer := server.NewMCPServer(
		"TimeMCP",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions("Time conversion and timezone utilities."),
	)

	// Add the get_current_time tool
	mcpServer.AddTool(
		mcp.NewTool("get_current_time",
			mcp.WithDescription("Get current time in a specific timezone or system timezone."),
			mcp.WithString("timezone",
				mcp.Description("The timezone to get the current time in. If not provided, system timezone is used."),
			),
		),
		wrapWithAuth(handleGetCurrentTime, "get_current_time", config),
	)

	// Add the convert_time tool
	mcpServer.AddTool(
		mcp.NewTool("convert_time",
			mcp.WithDescription("Convert time between timezones."),
			mcp.WithString("source_timezone",
				mcp.Description("Source timezone. Defaults to system timezone if not provided."),
				mcp.DefaultString(""),
			),
			mcp.WithString("time",
				mcp.Description("Time in 24-hour format (HH:MM). Defaults to current time if not provided."),
				mcp.DefaultString(""),
			),
			mcp.WithString("target_timezone",
				mcp.Description("Target timezone to convert the time to."),
				mcp.Required(),
			),
		),
		wrapWithAuth(handleConvertTime, "convert_time", config),
	)

	// Validate transport flag
	if *transportFlag != "stdio" && *transportFlag != "http" {
		log.Printf("Invalid transport mode: %s. Must be 'stdio' or 'http'\n", *transportFlag)
		os.Exit(1)
	}

	// Start the appropriate transport based on command-line flag
	if *transportFlag == "http" {
		log.Printf("Starting TimeMCP server with HTTP transport on %s%s\n", config.HTTPAddress, config.HTTPPath)
		if err := startHTTPServer(mcpServer, config); err != nil {
			log.Printf("HTTP server error: %v\n", err)
			os.Exit(1)
		}
	} else {
		log.Println("Starting TimeMCP server with stdio transport...")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Printf("Error starting server: %v\n", err)
			os.Exit(1)
		}
	}
}

// wrapWithAuth wraps a tool handler with authentication and logging
func wrapWithAuth(handler func(context.Context, mcp.CallToolRequest, *Config) (*mcp.CallToolResult, error), toolName string, config *Config) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		log.Printf("Calling tool '%s'...", toolName)

		// Check authentication for HTTP requests if enabled
		if httpMethod, ok := ctx.Value(httpMethodKey).(string); ok && httpMethod != "" {
			// This is an HTTP request, check if auth is required
			if config.AuthEnabled {
				if authError := getAuthError(ctx); authError != "" {
					log.Printf("Authentication failed for tool '%s': %s", toolName, authError)
					return mcp.NewToolResultError(fmt.Sprintf("Authentication required: %s", authError)), nil
				}

				// Log successful authentication if present
				if isAuthenticated(ctx) {
					userID, username, role := getUserInfo(ctx)
					log.Printf("Tool '%s' called by authenticated user %s (%s) with role %s", toolName, username, userID, role)
				} else if config.AuthEnabled {
					log.Printf("Authentication required for tool '%s' but not provided", toolName)
					return mcp.NewToolResultError("Authentication required"), nil
				}
			}
		}

		// Call the actual handler
		resp, err := handler(ctx, req, config)

		if err != nil {
			log.Printf("Tool '%s' failed: %v", toolName, err)
		} else {
			log.Printf("Tool '%s' completed successfully", toolName)
		}

		return resp, err
	}
}

// Handler for the get_current_time tool
func handleGetCurrentTime(ctx context.Context, request mcp.CallToolRequest, config *Config) (*mcp.CallToolResult, error) {
	timezoneStr := request.GetString("timezone", "")

	loc, err := loadTimezone(timezoneStr, config)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid timezone: %s", timezoneStr)), nil
	}

	now := time.Now().In(loc)
	response := fmt.Sprintf("Current time in %s: %s", loc.String(), now.Format("2006-01-02 15:04:05"))

	return mcp.NewToolResultText(response), nil
}

// Handler for the convert_time tool
func handleConvertTime(ctx context.Context, request mcp.CallToolRequest, config *Config) (*mcp.CallToolResult, error) {
	sourceTimezoneStr := request.GetString("source_timezone", "")
	timeStr := request.GetString("time", "")

	targetTimezoneStr, err := request.RequireString("target_timezone")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Set source timezone
	sourceLoc, err := loadTimezone(sourceTimezoneStr, config)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid source timezone: %s", sourceTimezoneStr)), nil
	}
	if sourceTimezoneStr == "" {
		sourceTimezoneStr = sourceLoc.String()
	}

	// Set target timezone
	targetLoc, err := loadTimezone(targetTimezoneStr, config)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid target timezone: %s", targetTimezoneStr)), nil
	}

	// Determine the time to convert
	var sourceTime time.Time
	if timeStr == "" {
		// Use current time if not provided
		sourceTime = time.Now().In(sourceLoc)
	} else {
		// Parse the provided time
		// We'll construct a full datetime string with today's date
		today := time.Now().In(sourceLoc).Format("2006-01-02")
		fullTimeStr := fmt.Sprintf("%s %s", today, timeStr)

		sourceTime, err = dateparse.ParseIn(fullTimeStr, sourceLoc)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid time format: %s. Please provide time in HH:MM format.", timeStr)), nil
		}
	}

	// Convert to target timezone
	targetTime := sourceTime.In(targetLoc)

	response := fmt.Sprintf(
		"Time conversion: %s in %s → %s in %s",
		sourceTime.Format("15:04"),
		sourceTimezoneStr,
		targetTime.Format("15:04"),
		targetTimezoneStr,
	)

	return mcp.NewToolResultText(response), nil
}
