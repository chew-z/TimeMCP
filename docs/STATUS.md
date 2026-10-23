---
title: TimeMCP status
date: 2026-10-23
status: active
tags: [status, go, mcp]
---

# Status

## Toolchain

| Component | Version |
| --- | --- |
| Go | 1.27.0 (toolchain go1.27.2) |
| `github.com/mark3labs/mcp-go` | v1.3.0 |
| `github.com/golang-jwt/jwt/v5` | v5.3.1 |
| `github.com/araddon/dateparse` | v0.0.0-20210429162001-6b43995a97de |
| `golang.org/x/term` | v0.47.0 (`golang.org/x/sys` v0.49.0 indirect) |
| golangci-lint | default configuration (`run_lint.sh`), 0 issues |

## Health

- `task check` is green: gofmt, `go vet`, golangci-lint, and the unit test suite.
- `go test -race ./...` is green.
- The stdio-only smoke client (`examples/test_client.go`) checks the legacy
  `2025-11-25` initialize handshake and exercises MCP `2026-07-28`
  (`server/discover`, `tools/list`, `tools/call`) with `_meta`.
  It calls every tool and fails on protocol or tool errors.
- The HTTP transport returns the `/health` payload and shuts down cleanly on SIGTERM.
- Deployment health is not monitored from this repository; the process runs behind nginx.

## Recent changes

### 2026-10-23 — OAuth 2.1 connector auth (issue #2)

- Ported `internal/oauth/` from Answer/Pushover/Alerts with TimeMCP identity,
  scope `time`, and realm `time-mcp`.
- Added opt-in `TIME_OAUTH_*` settings for public issuer/resource URLs, login
  credentials, and access/refresh TTLs (1h/720h).
  OAuth forces authentication on and requires a signing secret of at least
  32 bytes; invalid settings and authenticated CORS wildcards fail startup.
- Added HTTP 401 discovery challenges and 403 insufficient-scope responses
  when OAuth is on; static JWTs and the OAuth-off tool-error path still work.
  Health and OAuth routes bypass the MCP guard.
- Added `--hash-password` for PBKDF2 login hashes with terminal echo suppressed.
- Added `golang.org/x/term` v0.47.0 and `golang.org/x/sys` v0.49.0 indirect.
- Added package, config, signer, HTTP guard, password CLI, and OAuth end-to-end
  tests covering login, PKCE, token refresh, discovery, and MCP tool calls.
  `go vet ./...` and `go test -count=1 -race ./...` pass.

### 2026-10-23 — smoke client protocol update

- Smoke client moved to MCP `2026-07-28` with `server/discover` and `_meta`,
  retains one legacy `2025-11-25` initialize check, and now fails on tool errors.

### 2026-10-23 — describe_time timezone fix (issue #1)

- `formatSingle` and `formatMulti` normalize each candidate to `now`'s location,
  so inputs carrying their own offset (RFC 3339 `Z`) render the requested zone
  in `machine`, `human`, and `tense`, not just in `perspective`.
- Tests cover CEST, CET, the near-midnight date flip, and the multi-candidate path.

### 2026-10-23 — modernization pass

- Go directive raised from 1.25.5 to 1.27.0.
- mcp-go raised from v0.56.0 to v1.3.0 (API-compatible; MCP 2026-07-28 support).
- `go fix ./...` applied: `any`, `strings.CutPrefix`, `slices.Contains`, `strings.SplitSeq`,
  and promoted-field literals.
- Server logging migrated from `log` to `log/slog`.
- HTTP lifecycle modernized: `signal.NotifyContext`, `sync.WaitGroup.Go`, `errors.Is`,
  and a 10-second `ReadHeaderTimeout`.
- `/health` response now uses `encoding/json/v2`.
- `relative_time.go` renamed to `describe_time.go`; flags collected in a `cliOptions` struct.
- Server version collapsed into one `serverVersion` constant used by MCP initialization and `/health`.
- `CreateTokenCommand` now returns errors, and its user-facing output goes to stdout.
- README, CLAUDE.md, and AGENTS.md refreshed; `docs/` role documents created.
- `.gitignore`: `go.sum` is no longer ignored (the global `~/.gitignore` is overridden with `!go.sum`),
  and `/test_*` no longer hides `examples/test_client.go`.

## Next checkpoint

- Deploy with `TIME_OAUTH_*` set and register the claude.ai custom connector.
  nginx must forward the OAuth authorization routes and well-known discovery
  paths as well as the MCP endpoint.
- Re-run `task check` after any dependency change.
- Verify a live claude.ai custom-connector login once deployed.
