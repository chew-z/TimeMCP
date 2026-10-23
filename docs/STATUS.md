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
| golangci-lint | default configuration (`run_lint.sh`), 0 issues |

## Health

- `task check` is green: gofmt, `go vet`, golangci-lint, and the unit test suite.
- `go test -race ./...` is green.
- The stdio smoke client (`examples/test_client.go`) completes the handshake and calls every tool.
- The HTTP transport returns the `/health` payload and shuts down cleanly on SIGTERM.
- Deployment health is not monitored from this repository; the process runs behind nginx.

## Recent changes

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
- `run_release.sh` repaired: it requires a tag, builds `bin/mcp-time`, derives the repo NWO,
  uses `--generate-notes`, and no longer deletes a notes file.
- README, CLAUDE.md, and AGENTS.md refreshed; `docs/` role documents created.
- `.gitignore`: `go.sum` is no longer ignored (the global `~/.gitignore` is overridden with `!go.sum`),
  and `/test_*` no longer hides `examples/test_client.go`.

## Next checkpoint

- Review and commit the modernization diff; `go.sum` and `examples/test_client.go` need their first commit.
- Re-run `task check` after any dependency change.
- The repaired release script has passed `bash -n` and `shellcheck` but has not been run live.
