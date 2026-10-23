# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

TimeMCP is a Model Context Protocol (MCP) server that provides time and timezone conversion utilities.
It targets Go 1.27 and is built on `github.com/mark3labs/mcp-go` v1, and implements three core tools:

- `get_current_time`: Gets current time in specified timezone
- `convert_time`: Converts time between different timezones
- `describe_time`: Parses a flexible date/time string and describes how it relates to now,
  returning a machine-usable RFC 3339 timestamp alongside human-friendly prose.
  Supports both absolute dates and relative phrases (tomorrow, next Tuesday, in 2 hours, etc.).
  Ambiguous inputs return labeled candidates.

The opt-in OAuth 2.1 authorization server lives in `internal/oauth/`,
ported from Answer/Pushover/Alerts; `cli_password.go` provides password hashing.

## Development Commands

### Build and Run

```bash
task build        # Build binary to bin/mcp-time
task run          # Run server directly (stdio transport)
task test-client  # Build, then test server with example client
task clean        # Remove build artifacts
task all          # Download deps and build
task check        # Pre-commit gate: fmt:check, vet, lint, test
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
task test        # Run the unit test suite
go test -race ./...             # Same, with the race detector
task test-client # Legacy initialize check and MCP 2026-07-28 requests (stdio only)

# Test HTTP transport with curl
curl http://localhost:8080/health
```

Unit tests live beside the code (`*_test.go`).
The test client checks the legacy `2025-11-25` initialize handshake
and exercises MCP `2026-07-28` over stdio only
(`server/discover`, `tools/list`, `tools/call`) with `_meta`.
It calls every tool and fails on protocol or tool errors.
Update it when a tool changes.

## Architecture

### Core Structure

- **Modular implementation**: Server entry point and tool registration in `main.go`,
  HTTP transport in `http_server.go`, authentication and CORS origin matching in `auth.go`,
  configuration in `config.go`, the `describe_time` handler in `describe_time.go`,
  its parser in `flexible_parse.go`, and its prose renderer in `perspective.go`
- **OAuth connector auth**: `internal/oauth/` provides the OAuth 2.1 server,
  ported from Answer/Pushover/Alerts; `cli_password.go` reads passwords and
  prints PBKDF2 hashes for operator configuration
- **Dual Transport Support**: Both stdio and HTTP transports
- **Tool Handlers**: Each tool receives `mcp.CallToolRequest` and returns `mcp.CallToolResult`

### Key Dependencies

- `github.com/mark3labs/mcp-go/mcp` (v1): MCP protocol implementation
- `github.com/golang-jwt/jwt/v5`: JWT authentication for the HTTP transport
- `github.com/araddon/dateparse`: Flexible date parsing
- `golang.org/x/term`: Echo-free password input for `--hash-password`
- Standard library `time` package for timezone operations
- Standard library `log/slog` for structured logging

### Transport Modes

#### Stdio Transport (Default)

- JSON-RPC communication over stdin/stdout
- Suitable for direct MCP client integration
- No authentication required

#### HTTP Transport

- RESTful HTTP endpoints with JSON responses
- Supports CORS for web applications
- Optional JWT-based authentication and opt-in OAuth 2.1 connector login
- Health endpoint (`GET /health`); tool discovery uses MCP `tools/list`
- Graceful shutdown driven by `signal.NotifyContext`

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
  - `TIME_HTTP_SESSION_IDLE_TTL="5m"` (default: `5m`)
  - `TIME_HTTP_DISABLE_LOCALHOST_PROTECTION=true|false` (default: `true`) — disables mcp-go's built-in DNS-rebinding protection,
    which rejects requests whose `Host` header isn't loopback when the server listens on 127.0.0.1.
    Defaults to disabled because TimeMCP always runs behind a reverse proxy (e.g. nginx) that forwards the real `Host` header;
    that header is legitimately non-loopback in that setup, so the protection would otherwise 403 every proxied request.
    Set to `false` only if the HTTP transport is exposed directly with no reverse proxy in front of it.
- CORS:
  - `TIME_HTTP_CORS_ENABLED=true|false` (default: `false`)
  - `TIME_HTTP_CORS_ORIGINS="..."` (default: empty; no allowed origins)
- Authentication:
  - `TIME_AUTH_ENABLED=true|false` (default: `false`)
  - `TIME_AUTH_SECRET_KEY="your-256-bit-secret-key-here"` (required if auth enabled; ≥32 chars)
  - `TIME_AUTH_ISSUER="TimeMCP"` (default: `TimeMCP`)
  - `TIME_AUTH_AUDIENCE="TimeMCP-user"` (default: `TimeMCP-user`)

- OAuth:
  - `TIME_OAUTH_ENABLED=true|false` (default: `false`)
  - `TIME_OAUTH_ISSUER="https://karma.example/time"` (required when enabled)
  - `TIME_OAUTH_RESOURCE="https://karma.example/time/mcp"` (public MCP URL;
    required when enabled, independent of local `TIME_HTTP_PATH`)
  - `TIME_OAUTH_LOGIN_USER="tester"` (required when enabled)
  - `TIME_OAUTH_LOGIN_PASSWORD_HASH="..."` (from `--hash-password`;
    required when enabled)
  - `TIME_OAUTH_ACCESS_TTL="1h"` (default: `1h`)
  - `TIME_OAUTH_REFRESH_TTL="720h"` (default: `720h`; must exceed access TTL)

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
curl -H "Authorization: Bearer YOUR_TOKEN" http://localhost:8080/mcp
```

### OAuth connector

OAuth is opt-in via `TIME_OAUTH_ENABLED=true` and forces authentication on.
`TIME_AUTH_SECRET_KEY` must be at least 32 bytes when OAuth is enabled.
Invalid OAuth settings fail startup; empty booleans and TTLs use defaults.
The CORS wildcard rejection also applies when OAuth enables authentication.
Missing or invalid MCP tokens receive HTTP 401 plus `WWW-Authenticate`
with `resource_metadata` so connectors can discover the login flow.
Wrong OAuth scope receives HTTP 403 `insufficient_scope`.
Static JWTs remain accepted; with OAuth off, tool authentication keeps its
existing MCP tool-error behavior.

Generate the login password hash before configuring the server:

```bash
go run . --hash-password
```

OAuth clients, codes, and refresh tokens live in memory, so a restart requires
fresh connector login; static tokens keep working with unchanged auth settings.
For connector setup and nginx route forwarding, see the README OAuth subsection.

### HTTP Endpoints

- `GET /health` - Health check endpoint
- `POST /mcp` - Streamable HTTP MCP endpoint (configurable via `TIME_HTTP_PATH`)
- OAuth discovery (mounted only when OAuth is enabled):
  - `GET /.well-known/oauth-protected-resource` and its resource-path suffix
  - `GET /.well-known/oauth-authorization-server` and its issuer-path suffix
- OAuth endpoints: `GET /authorize`, `POST /login`, `POST /token`, and
  `POST /register` (dynamic client registration; authorization uses PKCE S256)
- `/health` remains unauthenticated in all modes

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

Three layers:

- **Unit tests** (`*_test.go`) cover parsing, formatting, CORS, and JWT validation.
  Run them with `task test` or `go test -race ./...`; `task check` includes them.
- **OAuth integration** (`oauth_e2e_test.go`) covers the full DCR → authorize →
  login → token → MCP tools call over `httptest`, plus refresh, static JWT
  fallback, and discovery documents.
- **Smoke test** (`examples/test_client.go`) checks the legacy `2025-11-25`
  initialize handshake and exercises MCP `2026-07-28` over stdio only
  (`server/discover`, `tools/list`, `tools/call`) with `_meta`.
  It calls every tool with realistic parameters
  and fails on protocol or tool errors.
  Run it with `task test-client`.

When a tool's contract changes, update both the unit tests and the smoke client.

## Further Instructions

## Rules for GOLANG

@./GOLANG.md

### Learning about Golang packages

@./GODOC.md

## Language server (LSP) is your superpower

@./LSP.md
