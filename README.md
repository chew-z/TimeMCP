# TimeMCP

A simple Model Control Protocol (MCP) server that provides time and timezone conversion tools.

## Features

The server provides the following tools:

### 1. `get_current_time`

Gets the current time in a specific timezone or the system timezone.

**Arguments:**
- `timezone` (string, optional): The timezone to get the current time in. If not provided, the system timezone is used.

**Example Response:**
```
Current time in Europe/Warsaw: 2025-04-09 15:30:45
```

### 2. `convert_time`

Converts time between different timezones.

**Arguments:**
- `source_timezone` (string, optional): Source timezone. Defaults to system timezone if not provided.
- `time` (string, optional): Time in 24-hour format (HH:MM). Defaults to current time if not provided.
- `target_timezone` (string, required): Target timezone to convert the time to.

**Example Response:**
```
Time conversion: 15:30 in Europe/Warsaw → 09:30 in America/New_York
```

## Usage

### Build

```bash
go build -o ./bin/mcp-time .
```

### Add to claude_desktop_config.json

```json
{                                                                                 "mcpServers": {
  "time": {
        "command": "/Path/To/Your/Bin/mcp-time"
      }
  }
}
```

### Using with MCP-compatible Clients

The server implements the Model Control Protocol, which means it can be used with any MCP-compatible client. The client will be able to:

1. Discover the available tools
2. Call the tools with the appropriate arguments
3. Receive formatted responses

## Dependencies

- `github.com/mark3labs/mcp-go`: Implementation of the Model Control Protocol
- `github.com/araddon/dateparse`: Flexible date parsing library

## Supported Timezones

The server supports all IANA time zone database entries like:
- `Europe/Warsaw`
- `America/New_York`
- `Asia/Tokyo`
- `Australia/Sydney`
- `Africa/Cairo`
- etc.

## Examples

### Getting Current Time in Tokyo

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "get_current_time",
    "arguments": {
      "timezone": "Asia/Tokyo"
    }
  }
}
```

### Converting Time

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "convert_time",
    "arguments": {
      "source_timezone": "Europe/Warsaw",
      "time": "14:30",
      "target_timezone": "America/Los_Angeles"
    }
  }
}
```

## HTTP Transport and CORS

- Run HTTP transport: `go run . --transport=http [--auth-enabled]`
- Health: `GET /health`, Capabilities: `GET /capabilities`, MCP: `POST {TIME_HTTP_PATH}/*` (default `"/mcp"`)

### CORS Behavior

- Default: CORS is disabled (`TIME_HTTP_CORS_ENABLED=false`).
- When enabled, no origins are allowed unless explicitly listed in `TIME_HTTP_CORS_ORIGINS`.
- `TIME_HTTP_CORS_ORIGINS` accepts a comma-separated allowlist. Supported forms:
  - Hostname: `example.com`
  - Host:port: `localhost:3000`, `127.0.0.1:8080`
  - Full URL: `https://app.example.com` (normalized to its host)
  - Wildcard subdomain: `*.example.com` (matches `example.com` and any subdomain)
- With `TIME_AUTH_ENABLED=true`, using `*` in `TIME_HTTP_CORS_ORIGINS` is rejected at startup.
- Preflight and response headers are returned only for allowed origins.

### Configuration Examples

- Development:
  - `TIME_HTTP_CORS_ENABLED=true`
  - `TIME_HTTP_CORS_ORIGINS="http://localhost:3000,http://127.0.0.1:5173"`
- Production:
  - `TIME_HTTP_CORS_ENABLED=true`
  - `TIME_HTTP_CORS_ORIGINS="https://yourdomain.com,https://app.yourdomain.com,*.trusted-partner.com"`
  - Avoid `*`; if possible, disable CORS for server-to-server use.

### Environment Variables

- `TIME_HTTP_CORS_ENABLED` (default: `false`)
- `TIME_HTTP_CORS_ORIGINS` (default: empty, meaning no allowed origins)
- `TIME_AUTH_ENABLED` (default: `false`)
- `TIME_AUTH_SECRET_KEY` (required if auth enabled; ≥32 chars)
- `TIME_HTTP_ADDRESS` (default: `":8080"`)
- `TIME_HTTP_PATH` (default: `"/mcp"`)

### Quick HTTP Checks

- `curl -i http://localhost:8080/health`
- With JWT: `curl -i -H "Authorization: Bearer $TOKEN" http://localhost:8080/capabilities`