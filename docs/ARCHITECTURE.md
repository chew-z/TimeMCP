---
title: TimeMCP architecture
date: 2026-10-23
status: active
tags: [architecture, go, mcp]
---

# TimeMCP architecture

TimeMCP is a single Go binary (`package main`) that exposes three read-only MCP tools.
It has no database and makes no outbound network calls:
every answer comes from the system clock, the IANA timezone database, and per-request configuration.

## Process layout

| File | Responsibility |
| --- | --- |
| `main.go` | flag parsing (`main.go:79`), MCP server construction, tool registration (`main.go:92`), transport selection |
| `config.go` | environment parsing and validation (`config.go:64`) |
| `http_server.go` | streamable HTTP transport, `/health` (`http_server.go:115`), CORS wrapper, graceful shutdown (`http_server.go:63`) |
| `auth.go` | JWT middleware (`auth.go:68`), claims validation (`auth.go:112`), token CLI, CORS origin matching (`auth.go:246`) |
| `describe_time.go` | `describe_time` tool handler (`describe_time.go:13`) |
| `flexible_parse.go` | relative and absolute date parsing (`flexible_parse.go:50`), ambiguity candidates |
| `perspective.go` | human prose, tense, and perspective rendering (`perspective.go:62`) |

## Tool surface

| Tool | Arguments | Handler |
| --- | --- | --- |
| `get_current_time` | `timezone?` | `handleGetCurrentTime` (`main.go:237`) |
| `convert_time` | `source_timezone?`, `time?`, `target_timezone` | `handleConvertTime` (`main.go:259`) |
| `describe_time` | `date`, `timezone?` | `handleDescribeTime` (`describe_time.go:13`) |

All handlers return `*mcp.CallToolResult`; user-facing failures use `mcp.NewToolResultError` rather than a Go error.

## describe_time pipeline

1. `parseFlexibleDate` (`flexible_parse.go:50`) trims the input and tries the relative grammar first.
2. `parseRelativeDate` (`flexible_parse.go:78`) checks the keyword phrases (`now`, `today`, `tomorrow`, `yesterday`),
   then walks the ordered `relativeMatchers` chain.
   `splitAtTime` pulls an optional `at <time>` suffix before matching.
3. `parseAbsoluteDate` (`flexible_parse.go:389`) strips a weekday prefix and ordinal suffixes, then defers to `dateparse`.
   Slash- and dot-separated numeric dates are parsed twice, month-first and day-first, to expose ambiguity.
4. Ambiguity generators add labeled candidates for a weekday mismatch, a dual numeric reading,
   and a year-less date more than 30 days in the past.
5. `formatDescribeTime` (`perspective.go:62`) renders a single result with `formatSingle` or labeled candidates with `formatMulti`,
   combining `formatHuman`, `tenseLabel`, and `perspectivePhrase` (`perspective.go:157`).

## Configuration

`NewConfig` in `config.go` reads the environment once at startup.
An invalid `TIME_DEFAULT_TIMEZONE` or `*` in `TIME_HTTP_CORS_ORIGINS` with auth enabled fails fast.
The full variable table lives in `README.md`.
`TIME_OAUTH_ENABLED` opts into `internal/oauth/`; OAuth settings are parsed
before CORS, force authentication on, and require a secret of at least 32 bytes.
`OAuthConfig()` validates public issuer/resource URLs, login credentials, and
access/refresh TTLs; invalid OAuth settings fail startup.

## Transports

### stdio (default)

`server.ServeStdio` owns stdin/stdout for JSON-RPC; logs go to stderr via `log/slog`.
No authentication is applied.

### HTTP

`server.NewStreamableHTTPServer` is mounted in a custom `http.ServeMux` that also serves `GET /health`.
The CORS wrapper is installed only when `TIME_HTTP_CORS_ENABLED=true`.
With OAuth off, JWT validation happens in the HTTP context function;
tool middleware returns the existing MCP error for unauthenticated calls.
With OAuth on, `buildHTTPHandler` assembles the flow:
CORS → OAuth guard → mcp-go → context auth → tool auth middleware.
The guard returns 401 with a resource-metadata challenge, or 403 for wrong scope;
both static and OAuth JWT profiles reach the same context/tool authentication.
`internal/oauth/` mounts discovery, authorize, login, token, and registration
routes on the same mux; those routes and `/health` bypass the MCP guard.
OAuth state is in memory, so restarts require fresh connector login.
nginx must forward well-known paths and issuer-prefixed OAuth routes as well
as the MCP endpoint.
Shutdown is driven by `signal.NotifyContext` with `TIME_HTTP_TIMEOUT` as the deadline.
`ReadHeaderTimeout` is fixed at 10 seconds.

## Logging

Server code uses structured `log/slog` calls to stderr.
The `--generate-token` CLI is the exception: its token and instructions go to stdout so they can be copied.

## Testing

- Unit tests sit beside the code and cover parsing, formatting, CORS, and JWT validation.
- `examples/test_client.go` checks the legacy `2025-11-25` initialize handshake
  and exercises MCP `2026-07-28` over stdio only
  (`server/discover`, `tools/list`, `tools/call`) with `_meta`.
  It calls every tool and fails on protocol or tool errors.
- `task check` runs `fmt:check`, `vet`, `lint`, and `test`.

## Deployment

TimeMCP normally runs behind an nginx reverse proxy that terminates TLS and forwards the real `Host` header.
That is why `TIME_HTTP_DISABLE_LOCALHOST_PROTECTION` defaults to `true`:
mcp-go's DNS-rebinding guard would otherwise reject every proxied request.
Disable the default only when the HTTP transport is exposed directly.
