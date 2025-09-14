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
make run         # Run server directly (stdio transport)
make test-client # Test server with example client
make clean       # Remove build artifacts
make all         # Download deps and build
```

### Transport Options
```bash
# Run with stdio transport (default)
go run . --transport=stdio

# Run with HTTP transport
go run . --transport=http

# Run with HTTP transport and authentication
go run . --transport=http --auth-enabled
```

### Testing
```bash
make test        # Run test suite
go run examples/test_client.go  # Test with example MCP client (stdio only)

# Test HTTP transport with curl
curl http://localhost:8080/health
curl http://localhost:8080/capabilities
```

The test client demonstrates full MCP handshake and tool invocation workflow for stdio transport.

## Architecture

### Core Structure
- **Modular implementation**: Main server logic in `main.go`, HTTP transport in `http_server.go`, authentication in `auth.go`, configuration in `config.go`
- **Dual Transport Support**: Both stdio and HTTP transports
- **Tool Handlers**: Each tool receives `mcp.CallToolRequest` and returns `mcp.CallToolResult`

### Key Dependencies
- `github.com/mark3labs/mcp-go/mcp`: MCP protocol implementation
- `github.com/araddon/dateparse`: Flexible date parsing
- Standard library `time` package for timezone operations

### Transport Modes

#### Stdio Transport (Default)
- JSON-RPC communication over stdin/stdout
- Suitable for direct MCP client integration
- No authentication required

#### HTTP Transport
- RESTful HTTP endpoints with JSON responses
- Supports CORS for web applications
- Optional JWT-based authentication
- Health and capabilities endpoints
- Graceful shutdown with signal handling

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
1. Create tool using `mcp.NewTool()` with parameter specifications using `mcp.WithString()`, `mcp.WithNumber()`, etc.
2. Use `mcp.Required()` for mandatory parameters and `mcp.DefaultString()` for optional ones
3. Implement handler function with signature `func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)`
4. Extract parameters using `req.GetString(name, default)` for optional or `req.RequireString(name)` for required parameters
5. Register tool and handler with `mcpServer.AddTool(toolDefinition, handlerFunction)`
6. Update `examples/test_client.go` to test the new tool
7. Follow the established error handling pattern with MCP-compliant responses

## HTTP Configuration

Environment Variables

- Timezone:
  - `TIME_DEFAULT_TIMEZONE="UTC"` (default: system timezone)
- HTTP:
  - `TIME_HTTP_ADDRESS=":8080"` (default: `:8080`)
  - `TIME_HTTP_PATH="/mcp"` (default: `/mcp`)
  - `TIME_HTTP_STATELESS=true|false` (default: `false`)
  - `TIME_HTTP_HEARTBEAT="30s"` (default: `30s`)
  - `TIME_HTTP_TIMEOUT="30s"` (default: `30s`)
- CORS:
  - `TIME_HTTP_CORS_ENABLED=true|false` (default: `false`)
  - `TIME_HTTP_CORS_ORIGINS="..."` (default: empty; no allowed origins)
- Authentication:
  - `TIME_AUTH_ENABLED=true|false` (default: `false`)
  - `TIME_AUTH_SECRET_KEY="your-256-bit-secret-key-here"` (required if auth enabled; ≥32 chars)
  - `TIME_AUTH_ISSUER="TimeMCP"` (default: `TimeMCP`)
  - `TIME_AUTH_AUDIENCE="TimeMCP-user"` (default: `TimeMCP-user`)

### JWT Authentication

#### Generate Token
```bash
# Set secret key
export TIME_AUTH_SECRET_KEY="your-256-bit-secret-key-here"

# Generate token
go run . --generate-token --token-user-id="user123" --token-username="john" --token-role="admin"
```

#### Use Token
```bash
# Include in HTTP requests
curl -H "Authorization: Bearer YOUR_TOKEN" http://localhost:8080/mcp/tools/call
```

### HTTP Endpoints
- `GET /health` - Health check endpoint
- `GET /capabilities` - Available tools and their schemas
- `POST /mcp/*` - MCP protocol endpoints (tools, resources, etc.)

## CORS Security Configuration

Security Warning (Updated)

- CORS is disabled by default. If you enable it, the server denies all cross-origin requests unless you provide an explicit allowlist via `TIME_HTTP_CORS_ORIGINS`.
- When JWT auth is enabled (`TIME_AUTH_ENABLED=true`), using `*` in `TIME_HTTP_CORS_ORIGINS` is rejected at startup for security.
- Prefer stdio transport for Claude Desktop or local tooling whenever possible.
- Do not expose the HTTP transport publicly without:
  - Strong JWT secrets (≥32 chars) and short token lifetimes;
  - An explicit, minimal CORS allowlist; and
  - TLS termination and network controls.

Allowed Origin Formats

- Hostname: `example.com`
- Host:port: `localhost:3000`, `127.0.0.1:8080`
- Full URL: `https://app.example.com` (normalized to host)
- Wildcard subdomains: `*.example.com` (matches `example.com` and any subdomain)
- Wildcard all: `*` (only allowed when auth is disabled; not recommended)

Recommended Settings

- Production:
  - `TIME_HTTP_CORS_ENABLED=true`
  - `TIME_HTTP_CORS_ORIGINS="https://yourdomain.com,https://app.yourdomain.com,*.trusted-partner.com"`
  - Avoid `*`; disable CORS entirely if using server-to-server.
- Development:
  - `TIME_HTTP_CORS_ENABLED=true`
  - `TIME_HTTP_CORS_ORIGINS="http://localhost:3000,http://127.0.0.1:5173"`

Notes

- Preflight (`OPTIONS`) and `Access-Control-Allow-*` headers are added only when the request’s `Origin` matches the allowlist.
- If `TIME_HTTP_CORS_ENABLED=true` and `TIME_HTTP_CORS_ORIGINS` is empty, no origins are allowed (effectively closed).
- If `TIME_AUTH_ENABLED=true` and `TIME_HTTP_CORS_ORIGINS` contains `*`, the server fails to start with a clear error.

## Timezone Configuration Examples

```bash
# Use UTC as default timezone
export TIME_DEFAULT_TIMEZONE="UTC"
go run . --transport=http

# Use New York timezone as default
export TIME_DEFAULT_TIMEZONE="America/New_York"
go run .

# Use Tokyo timezone as default
export TIME_DEFAULT_TIMEZONE="Asia/Tokyo"
go run .

# Use system timezone (default behavior)
# Don't set TIME_DEFAULT_TIMEZONE or set it to empty
unset TIME_DEFAULT_TIMEZONE
go run .
```

**How it works:**
- When `timezone` parameter is provided in tool calls, it takes precedence
- When `timezone` parameter is empty/missing, `TIME_DEFAULT_TIMEZONE` is used
- If `TIME_DEFAULT_TIMEZONE` is not set, system timezone is used
- Supports all IANA timezone identifiers (e.g., "Europe/London", "Asia/Shanghai")

## Testing Strategy

The project uses a comprehensive test client approach rather than unit tests. The `examples/test_client.go`:
- Demonstrates complete MCP handshake
- Tests both tools with realistic parameters
- Validates JSON-RPC communication flow
- Serves as documentation for MCP integration
- **Note**: Currently supports stdio transport only

## Further Instructions

- @./GOLANG.md
- @./GODOC.md
