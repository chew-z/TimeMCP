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

Use `--hash-password` to read a password and print its PBKDF2 hash, or
`--generate-token` to mint a static bearer token.
These credential flags exit before server configuration is loaded and cannot
be used together.

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

### OAuth 2.1 (claude.ai custom connectors)

Enable OAuth with `TIME_OAUTH_ENABLED=true` for claude.ai, Claude Desktop,
and mobile custom connectors, or Claude Code `/mcp` login.
OAuth is opt-in; existing stdio and static JWT setups keep working.

Set these environment variables:

- `TIME_OAUTH_ENABLED`: default `false`.
- `TIME_OAUTH_ISSUER`: required public base URL, including any nginx prefix,
  for example `https://karma.example/time`.
- `TIME_OAUTH_RESOURCE`: required public MCP URL, for example
  `https://karma.example/time/mcp`.
  This is the public URL; `TIME_HTTP_PATH` is the local endpoint path.
- `TIME_OAUTH_LOGIN_USER`: required login user name supplied by the operator.
- `TIME_OAUTH_LOGIN_PASSWORD_HASH`: required PBKDF2 hash produced by
  `mcp-time -hash-password`.
- `TIME_OAUTH_ACCESS_TTL`: default `1h`.
- `TIME_OAUTH_REFRESH_TTL`: default `720h`; must exceed the access TTL.

Generate the password hash without putting the password in a command argument:

```bash
bin/mcp-time -hash-password
```

Enabling OAuth forces authentication on, even if `TIME_AUTH_ENABLED=false`.
`TIME_AUTH_SECRET_KEY` must be at least 32 bytes; it signs OAuth access tokens.
Invalid OAuth settings fail startup.
Empty OAuth boolean and TTL values use their defaults.
With CORS enabled, `*` in `TIME_HTTP_CORS_ORIGINS` is rejected.

OAuth mounts these routes at the server root:

- `/.well-known/oauth-protected-resource[/<resource path>]`
- `/.well-known/oauth-authorization-server[/<issuer path>]`
- `/authorize`, `/login`, `/token`, and `/register`

The well-known routes serve both bare and path-suffixed discovery documents.
`/register` supports dynamic client registration; authorization uses PKCE S256.
With OAuth on, missing or invalid tokens receive HTTP 401 with
`WWW-Authenticate: Bearer ... resource_metadata="..."`.
That challenge makes claude.ai start the OAuth flow.
A valid OAuth token with the wrong scope receives HTTP 403 `insufficient_scope`.
With OAuth off, unauthenticated tool calls still return an MCP tool error,
as before.
`/health` stays unauthenticated.

Create a custom connector with the public MCP URL:

```text
https://karma.example/time/mcp
```

Leave Client ID and Client Secret empty.
Log in with the user name and password supplied by the operator.

For Claude Code, add this to the project `.mcp.json` without headers, then run
`/mcp` to complete OAuth login:

```json
{
  "mcpServers": {
    "time": {
      "type": "http",
      "url": "https://karma.example/time/mcp"
    }
  }
}
```

Alternatively, generate a static bearer token with `--generate-token`
(see [JWT authentication](#jwt-authentication)) and configure it as a header:

```json
{
  "mcpServers": {
    "time": {
      "type": "http",
      "url": "https://karma.example/time/mcp",
      "headers": {
        "Authorization": "Bearer <operator-issued-token>"
      }
    }
  }
}
```

The server stores OAuth state (registered clients, codes, and refresh tokens)
in memory, so a server restart requires a fresh login from the connector.
Static `TIME_AUTH` bearer tokens keep working across restarts as long as the
signing secret, issuer, and audience stay the same.

If nginx strips `/time`, proxy `/time/authorize`, `/time/login`, `/time/token`,
and `/time/register` to the corresponding server-root routes too.
Also forward host-root `/.well-known/*` requests, preserving the derived paths
such as `/.well-known/oauth-protected-resource/time/mcp`.
The server-root authorization routes are published with the issuer prefix.

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
| `TIME_OAUTH_ENABLED` | `false` | Enable OAuth connector login |
| `TIME_OAUTH_ISSUER` | empty | Public base URL, including any nginx prefix |
| `TIME_OAUTH_RESOURCE` | empty | Public MCP URL |
| `TIME_OAUTH_LOGIN_USER` | empty | Operator-supplied login user name |
| `TIME_OAUTH_LOGIN_PASSWORD_HASH` | empty | Hash from `--hash-password` |
| `TIME_OAUTH_ACCESS_TTL` | `1h` | Access token lifetime |
| `TIME_OAUTH_REFRESH_TTL` | `720h` | Refresh lifetime; must exceed access TTL |

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
- `golang.org/x/term`: echo-free password input for `--hash-password`

## Supported timezones

Any IANA timezone identifier works, for example `Europe/Warsaw`,
`America/New_York`, `Asia/Tokyo`, `Australia/Sydney`, or `Africa/Cairo`.
