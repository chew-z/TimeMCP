package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/araddon/dateparse"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// loadTimezone loads a timezone location, defaulting to system timezone if empty
func loadTimezone(tzStr string) (*time.Location, error) {
	if tzStr == "" {
		return time.Local, nil
	}
	return time.LoadLocation(tzStr)
}

func main() {
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
		handleGetCurrentTime,
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
		handleConvertTime,
	)

	// Start the server
	log.Println("Starting TimeMCP server...")
	if err := server.ServeStdio(mcpServer); err != nil {
		log.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
}

// Handler for the get_current_time tool
func handleGetCurrentTime(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	timezoneStr := request.GetString("timezone", "")

	loc, err := loadTimezone(timezoneStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid timezone: %s", timezoneStr)), nil
	}

	now := time.Now().In(loc)
	response := fmt.Sprintf("Current time in %s: %s", loc.String(), now.Format("2006-01-02 15:04:05"))
	
	return mcp.NewToolResultText(response), nil
}

// Handler for the convert_time tool
func handleConvertTime(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sourceTimezoneStr := request.GetString("source_timezone", "")
	timeStr := request.GetString("time", "")
	
	targetTimezoneStr, err := request.RequireString("target_timezone")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Set source timezone
	sourceLoc, err := loadTimezone(sourceTimezoneStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid source timezone: %s", sourceTimezoneStr)), nil
	}
	if sourceTimezoneStr == "" {
		sourceTimezoneStr = sourceLoc.String()
	}

	// Set target timezone
	targetLoc, err := loadTimezone(targetTimezoneStr)
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
