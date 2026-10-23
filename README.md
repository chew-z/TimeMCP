# TimeMCP

A Model Context Protocol (MCP) server that provides time and timezone tools.
It runs over stdio (default) or streamable HTTP and is built on
[`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go).

## Tools

### `get_current_time`

Returns the current time in a specific timezone or the system timezone.

| Argument | Type | Required | Description |
| --- | --- | --- | --- |
| `timezone` | string | no | IANA timezone; defaults to `TIME_DEFAULT_TIMEZONE`, then the system timezone |

Example response:

```text
Current time in Europe/Warsaw (CEST, UTC+02:00): Monday, 2025-04-09 15:30:45
```

### `convert_time`

Converts a time from one timezone to another.

| Argument | Type | Required | Description |
| --- | --- | --- | --- |
| `source_timezone` | string | no | Source IANA timezone; defaults to the configured default |
| `time` | string | no | Time in `HH:MM` format; defaults to now |
| `target_timezone` | string | yes | Target IANA timezone |

Example response:

```text
Time conversion: 2025-04-09 15:30 in Europe/Warsaw → 2025-04-09 09:30 in America/New_York
```

### `describe_time`

Parses a flexible date/time string and describes how it relates to now.
It returns a machine-usable RFC 3339 timestamp plus human-friendly prose,
so the caller never has to do date arithmetic.

Supported inputs include absolute dates (`July 4th, 1776`, `22:08`,
`Thursday, July 20 2026`) and relative phrases (`now`, `today`, `tomorrow`,
`yesterday`, `next/last/this <weekday>`, bare `<weekday>`,
`in N minutes/hours/days/weeks/months/years`, `N ... ago`,
`next/last week/month/year`), optionally suffixed with `at <time>`.

Ambiguous inputs return labeled candidates instead of guessing:
a weekday that does not match the date, `mm/dd` versus `dd/mm`,
and a year-less date more than 30 days in the past.

| Argument | Type | Required | Description |
| --- | --- | --- | --- |
| `date` | string | yes | Date/time phrase to interpret |
| `timezone` | string | no | Timezone used to anchor "now"; defaults as above |

Example response:

```text
input:       "tomorrow at 5pm"
machine:     2026-10-24T17:00:00-04:00
human:       tomorrow at 5:00 PM
tense:       future
perspective: tomorrow evening
```

## Build and run

The `Taskfile.yml` wraps the common commands; `go` works directly too.

```bash
task build                       # go build -o bin/mcp-time .
task run                         # stdio transport
task run:http                    # HTTP transport on :8080
task run:http:auth               # HTTP transport with JWT auth
task test                        # unit tests
task test-client                 # stdio smoke test via examples/test_client.go
task check                       # fmt:check, vet, lint, test
```

## Client configuration

Claude Desktop (stdio transport):

```json
{
  "mcpServers": {
    "time": {
      "command": "/Path/To/Your/Bin/mcp-time"
    }
  }
}
```

Any MCP-compatible client can discover the tools, call them, and read the
formatted responses.

## HTTP transport

Start the server with `--transport=http` (optionally `--auth-enabled`).
The MCP endpoint is `POST {TIME_HTTP_PATH}` (default `/mcp`).
Health checks live at `GET /health`; there is no separate capabilities endpoint
(tool discovery goes through MCP `tools/list`).

### CORS

CORS is disabled by default.
When enabled, no origin is allowed unless it is listed explicitly.

`TIME_HTTP_CORS_ORIGINS` accepts a comma-separated allowlist:

- Hostname: `example.com`
- Host and port: `localhost:3000`, `127.0.0.1:8080`
- Full URL: `https://app.example.com` (normalized to its host)
- Wildcard subdomain: `*.example.com` (matches `example.com` and any subdomain)
- `*` (only when authentication is disabled)

With `TIME_AUTH_ENABLED=true`, a `*` entry is rejected at startup.
Preflight and response headers are emitted only for allowed origins.

### JWT authentication

Generate a token (the secret must be at least 32 characters):

```bash
export TIME_AUTH_SECRET_KEY="your-256-bit-secret-key-here"
go run . --generate-token \
  --token-user-id="user123" --token-username="john" --token-role="admin"
```

Send it with each HTTP request:

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/mcp
```

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `TIME_DEFAULT_TIMEZONE` | system timezone | Default IANA timezone for all tools |
| `TIME_HTTP_ADDRESS` | `:8080` | HTTP listen address |
| `TIME_HTTP_PATH` | `/mcp` | MCP endpoint path |
| `TIME_HTTP_STATELESS` | `false` | Run the streamable HTTP transport without sessions |
| `TIME_HTTP_HEARTBEAT` | `30s` | SSE heartbeat interval |
| `TIME_HTTP_TIMEOUT` | `30s` | Read, write, and shutdown timeout |
| `TIME_HTTP_SESSION_IDLE_TTL` | `5m` | Idle session eviction |
| `TIME_HTTP_DISABLE_LOCALHOST_PROTECTION` | `true` | Disable DNS-rebinding checks (TimeMCP runs behind a reverse proxy) |
| `TIME_HTTP_CORS_ENABLED` | `false` | Enable the CORS wrapper |
| `TIME_HTTP_CORS_ORIGINS` | empty | Comma-separated origin allowlist |
| `TIME_AUTH_ENABLED` | `false` | Require a JWT on HTTP tool calls |
| `TIME_AUTH_SECRET_KEY` | empty | HMAC secret, required when auth is enabled |
| `TIME_AUTH_ISSUER` | `TimeMCP` | Expected JWT issuer |
| `TIME_AUTH_AUDIENCE` | `TimeMCP-user` | Expected JWT audience |

## Development

- Unit tests live beside the code (`*_test.go`); run them with `task test` or
  `go test -race ./...`.
- `examples/test_client.go` checks the legacy `2025-11-25` initialize handshake
  and exercises MCP `2026-07-28` over stdio only
  (`server/discover`, `tools/list`, `tools/call`) with `_meta`.
  It calls every tool and fails on protocol or tool errors.
- `docs/ARCHITECTURE.md` explains how the pieces fit, `docs/STATUS.md` records
  the current toolchain and health, and `docs/OPEN_ITEMS.md` lists known debt.
- Superseded docs and old reports belong in `docs/retired/` (tracked).
  New reports belong in `docs/reports/` (gitignored).

## Dependencies

- `github.com/mark3labs/mcp-go` v1: MCP protocol and transports
- `github.com/golang-jwt/jwt/v5`: JWT authentication
- `github.com/araddon/dateparse`: flexible absolute date parsing

## Supported timezones

Any IANA timezone identifier works, for example `Europe/Warsaw`,
`America/New_York`, `Asia/Tokyo`, `Australia/Sydney`, or `Africa/Cairo`.
