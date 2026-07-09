package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// handleDescribeTime returns a handler for the describe_time tool.
func handleDescribeTime(config *Config) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dateStr, err := request.RequireString("date")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		timezoneStr := request.GetString("timezone", "")

		loc, err := loadTimezone(timezoneStr, config)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid timezone: %s", timezoneStr)), nil
		}

		now := time.Now().In(loc)

		candidates, ambNote, err := parseFlexibleDate(dateStr, now)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		output := formatDescribeTime(dateStr, candidates, ambNote, now)
		return mcp.NewToolResultText(output), nil
	}
}
