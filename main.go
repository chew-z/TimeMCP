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
	if err := run(); err != nil {
		log.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	transportFlag, authEnabledFlag, generateTokenFlag, tokenUserIDFlag, tokenUsernameFlag, tokenRoleFlag, tokenExpirationFlag := setupFlags()

	if *generateTokenFlag {
		secretKey := os.Getenv("TIME_AUTH_SECRET_KEY")
		CreateTokenCommand(secretKey, *tokenUserIDFlag, *tokenUsernameFlag, *tokenRoleFlag, *tokenExpirationFlag)
		return nil
	}

	config, err := NewConfig()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	if *authEnabledFlag {
		config.AuthEnabled = true
		log.Println("Authentication feature enabled via command line flag")
	}

	mcpServer := server.NewMCPServer(
		"TimeMCP",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions("Time conversion and timezone utilities."),
	)

	mcpServer.Use(loggingMiddleware(), authMiddleware(config))
	addTools(mcpServer, config)

	return startServer(mcpServer, config, transportFlag)
}

func setupFlags() (*string, *bool, *bool, *string, *string, *string, *int) {
	transportFlag := flag.String("transport", "stdio", "Transport mode: 'stdio' (default) or 'http'")
	authEnabledFlag := flag.Bool("auth-enabled", false, "Enable JWT authentication for HTTP transport")
	generateTokenFlag := flag.Bool("generate-token", false, "Generate a JWT token and exit")
	tokenUserIDFlag := flag.String("token-user-id", "user1", "User ID for token generation")
	tokenUsernameFlag := flag.String("token-username", "admin", "Username for token generation")
	tokenRoleFlag := flag.String("token-role", "admin", "Role for token generation")
	tokenExpirationFlag := flag.Int("token-expiration", 744, "Token expiration in hours (default: 744 = 31 days)")
	flag.Parse()
	return transportFlag, authEnabledFlag, generateTokenFlag, tokenUserIDFlag, tokenUsernameFlag, tokenRoleFlag, tokenExpirationFlag
}

func addTools(mcpServer *server.MCPServer, config *Config) {
	mcpServer.AddTool(
		mcp.NewTool("get_current_time",
			mcp.WithDescription("Get current time in a specific timezone or system timezone."),
			mcp.WithString("timezone",
				mcp.Description("The timezone to get the current time in. If not provided, system timezone is used."),
			),
			mcp.WithTitleAnnotation("Get Current Time"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		),
		handleGetCurrentTime(config),
	)

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
			mcp.WithTitleAnnotation("Convert Time Between Timezones"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		),
		handleConvertTime(config),
	)
}

func startServer(mcpServer *server.MCPServer, config *Config, transportFlag *string) error {
	if *transportFlag != "stdio" && *transportFlag != "http" {
		return fmt.Errorf("invalid transport mode: %s. Must be 'stdio' or 'http'", *transportFlag)
	}

	if *transportFlag == "http" {
		log.Printf("Starting TimeMCP server with HTTP transport on %s%s\n", config.HTTPAddress, config.HTTPPath)
		if err := startHTTPServer(mcpServer, config); err != nil {
			return fmt.Errorf("HTTP server error: %w", err)
		}
	} else {
		log.Println("Starting TimeMCP server with stdio transport...")
		if err := server.ServeStdio(mcpServer); err != nil {
			return fmt.Errorf("error starting server: %w", err)
		}
	}
	return nil
}

// loggingMiddleware logs tool calls and their results
func loggingMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			toolName := req.Params.Name
			log.Printf("Calling tool '%s'...", toolName)

			result, err := next(ctx, req)

			if err != nil {
				log.Printf("Tool '%s' failed: %v", toolName, err)
			} else {
				log.Printf("Tool '%s' completed successfully", toolName)
			}

			return result, err
		}
	}
}

// authMiddleware checks authentication for HTTP requests
func authMiddleware(config *Config) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if authErr := checkAuth(ctx, req.Params.Name, config); authErr != nil {
				return authErr, nil
			}
			return next(ctx, req)
		}
	}
}

func checkAuth(ctx context.Context, toolName string, config *Config) *mcp.CallToolResult {
	httpMethod, ok := ctx.Value(httpMethodKey).(string)
	if !ok || httpMethod == "" {
		return nil // Not an HTTP request, no auth check
	}

	if !config.AuthEnabled {
		return nil // Auth is not enabled
	}

	if authError := getAuthError(ctx); authError != "" {
		log.Printf("Authentication failed for tool '%s': %s", toolName, authError)
		return mcp.NewToolResultError(fmt.Sprintf("Authentication required: %s", authError))
	}

	if !isAuthenticated(ctx) {
		log.Printf("Authentication required for tool '%s' but not provided", toolName)
		return mcp.NewToolResultError("Authentication required")
	}

	userID, username, role := getUserInfo(ctx)
	log.Printf("Tool '%s' called by authenticated user %s (%s) with role %s", toolName, username, userID, role)
	return nil
}

// handleGetCurrentTime returns a handler for the get_current_time tool
func handleGetCurrentTime(config *Config) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		timezoneStr := request.GetString("timezone", "")

		loc, err := loadTimezone(timezoneStr, config)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid timezone: %s", timezoneStr)), nil
		}

		now := time.Now().In(loc)
		response := fmt.Sprintf("Current time in %s (%s, UTC%s): %s",
			loc.String(),
			now.Format("MST"),
			now.Format("-07:00"),
			now.Format("Monday, 2006-01-02 15:04:05"),
		)

		return mcp.NewToolResultText(response), nil
	}
}

// handleConvertTime returns a handler for the convert_time tool
func handleConvertTime(config *Config) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			sourceTime.Format("2006-01-02 15:04"),
			sourceTimezoneStr,
			targetTime.Format("2006-01-02 15:04"),
			targetTimezoneStr,
		)

		return mcp.NewToolResultText(response), nil
	}
}
