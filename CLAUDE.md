# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

TimeMCP is a Model Context Protocol (MCP) server that provides time and timezone conversion utilities. It's built on the `github.com/mark3labs/mcp-go` framework and implements two core tools:
- `get_current_time`: Gets current time in specified timezone
- `convert_time`: Converts time between different timezones

## Development Commands

### Build and Run
```bash
make build       # Build binary to bin/mcp-time
make run         # Run server directly
make test-client # Test server with example client
make clean       # Remove build artifacts
make all         # Download deps and build
```

### Testing
```bash
make test        # Run test suite
go run examples/test_client.go  # Test with example MCP client
```

The test client demonstrates full MCP handshake and tool invocation workflow.

## Architecture

### Core Structure
- **Single-file implementation**: All server logic in `main.go` (appropriate for this scope)
- **MCP Protocol**: JSON-RPC communication over stdin/stdout
- **Tool Handlers**: Each tool receives `mcp.CallToolRequest` and returns `mcp.CallToolResult`

### Key Dependencies
- `github.com/mark3labs/mcp-go/mcp`: MCP protocol implementation
- `github.com/araddon/dateparse`: Flexible date parsing
- Standard library `time` package for timezone operations

### Timezone Handling
- Uses IANA timezone database via `time.LoadLocation()`
- Defaults to system timezone when not specified
- Helper function `loadTimezone()` centralizes timezone loading logic
- Supports all standard timezone identifiers (e.g., "Europe/Warsaw", "America/New_York")

### Error Handling
- Returns MCP-compliant errors via `mcp.NewToolResultError()`
- Validates timezone strings before processing
- Handles invalid time formats gracefully

## Adding New Tools

When adding new time-related tools:
1. Add tool declaration in the `tools` slice with proper parameter schema
2. Implement handler function following the pattern of existing handlers
3. Register handler in the switch statement within `handleToolCall()`
4. Update `examples/test_client.go` to test the new tool
5. Follow the established error handling pattern with MCP-compliant responses

## Testing Strategy

The project uses a comprehensive test client approach rather than unit tests. The `examples/test_client.go`:
- Demonstrates complete MCP handshake
- Tests both tools with realistic parameters
- Validates JSON-RPC communication flow
- Serves as documentation for MCP integration