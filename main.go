package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/araddon/dateparse"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const serverVersion = "1.0.0"

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
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	opts := parseFlags()

	if opts.GenerateToken {
		secretKey := os.Getenv("TIME_AUTH_SECRET_KEY")
		return CreateTokenCommand(secretKey, opts.TokenUserID, opts.TokenUsername, opts.TokenRole, opts.TokenExpirationHours)
	}

	config, err := NewConfig()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	if opts.AuthEnabled {
		config.AuthEnabled = true
		slog.Info("Authentication feature enabled via command line flag")
	}

	mcpServer := server.NewMCPServer(
		"TimeMCP",
		serverVersion,
		server.WithToolCapabilities(true),
		server.WithInstructions("Time conversion and timezone utilities."),
	)

	mcpServer.Use(loggingMiddleware(), authMiddleware(config))
	addTools(mcpServer, config)

	return startServer(mcpServer, config, opts.Transport)
}

type cliOptions struct {
	Transport            string
	AuthEnabled          bool
	GenerateToken        bool
	TokenUserID          string
	TokenUsername        string
	TokenRole            string
	TokenExpirationHours int
}

func parseFlags() cliOptions {
	var opts cliOptions
	flag.StringVar(&opts.Transport, "transport", "stdio", "Transport mode: 'stdio' (default) or 'http'")
	flag.BoolVar(&opts.AuthEnabled, "auth-enabled", false, "Enable JWT authentication for HTTP transport")
	flag.BoolVar(&opts.GenerateToken, "generate-token", false, "Generate a JWT token and exit")
	flag.StringVar(&opts.TokenUserID, "token-user-id", "user1", "User ID for token generation")
	flag.StringVar(&opts.TokenUsername, "token-username", "admin", "Username for token generation")
	flag.StringVar(&opts.TokenRole, "token-role", "admin", "Role for token generation")
	flag.IntVar(&opts.TokenExpirationHours, "token-expiration", 744, "Token expiration in hours (default: 744 = 31 days)")
	flag.Parse()
	return opts
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

	mcpServer.AddTool(
		mcp.NewTool("describe_time",
			mcp.WithDescription("Parse a flexible date/time string and describe how it relates to now. "+
				"Returns a machine-usable RFC 3339 timestamp alongside human-friendly prose and a perspective phrase, "+
				"so the caller never needs to do date arithmetic. Supports both absolute dates (e.g. "+
				"\"July 4th, 1776\", \"22:08\", \"Thursday, July 20 2026\") and relative phrases "+
				"(\"now\", \"today\", \"tomorrow\", \"yesterday\", \"next/last/this <weekday>\", "+
				"bare \"<weekday>\", \"in N minutes/hours/days/weeks/months/years\", \"N ... ago\", "+
				"\"next/last week/month/year\", optionally suffixed with \"at <time>\"). "+
				"Ambiguous inputs (weekday mismatch, mm/dd vs dd/mm, year-less dates far in the past) "+
				"return labeled candidates instead of guessing."),
			mcp.WithString("date",
				mcp.Description("The date/time string to describe, e.g. \"July 4th, 1776\", \"22:08\", \"tomorrow\", or \"next tuesday at 5pm\"."),
				mcp.Required(),
			),
			mcp.WithString("timezone",
				mcp.Description("Timezone to interpret and anchor 'now' in. If not provided, system timezone is used."),
			),
			mcp.WithTitleAnnotation("Describe Time Relative to Now"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		),
		handleDescribeTime(config),
	)
}

func startServer(mcpServer *server.MCPServer, config *Config, transport string) error {
	if transport != "stdio" && transport != "http" {
		return fmt.Errorf("invalid transport mode: %s. Must be 'stdio' or 'http'", transport)
	}

	if transport == "http" {
		slog.Info("starting TimeMCP server", "transport", "http", "address", config.HTTPAddress, "path", config.HTTPPath)
		if err := startHTTPServer(mcpServer, config); err != nil {
			return fmt.Errorf("HTTP server error: %w", err)
		}
	} else {
		slog.Info("starting TimeMCP server", "transport", "stdio")
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
			slog.Info("calling tool", "tool", toolName)

			result, err := next(ctx, req)

			if err != nil {
				slog.Error("tool failed", "tool", toolName, "error", err)
			} else {
				slog.Info("tool completed", "tool", toolName)
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
		slog.Warn("authentication failed", "tool", toolName, "error", authError)
		return mcp.NewToolResultError(fmt.Sprintf("Authentication required: %s", authError))
	}

	if !isAuthenticated(ctx) {
		slog.Warn("authentication required", "tool", toolName)
		return mcp.NewToolResultError("Authentication required")
	}

	userID, username, role := getUserInfo(ctx)
	slog.Info("tool called by authenticated user", "tool", toolName, "username", username, "user_id", userID, "role", role)
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
			today := time.Now().In(sourceLoc).Format(time.DateOnly)
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
