---
title: TimeMCP project handoff
date: 2026-10-23
status: active
tags: [handoff, roadmap, mcp, go]
---

# TimeMCP project handoff

Self-contained handoff for a session with no prior conversation context.
Read this file, then `ARCHITECTURE.md`, `STATUS.md`, and `OPEN_ITEMS.md`.

## What this project is

TimeMCP is a small Go MCP server that exposes three read-only tools:
`get_current_time`, `convert_time`, and `describe_time`.
It runs over stdio (default) or streamable HTTP with optional JWT auth and CORS.
There is no database and no outbound network dependency; answers come from the system clock,
the IANA timezone database, and environment configuration.

Repository root: `/Users/rrj/Projekty/Go/src/TimeMCP` on GoldenAir.

## Working contract

1. `task check` is the gate; a red gate is not done.
2. Read `GOLANG.md` before editing Go; standard library first, `log/slog` for logging, no ignored errors.
3. Delegate implementation typing to Codex; verify its diffs and run the gate locally yourself.
4. Documents live in `docs/`.
   Superseded docs and old reports go to `docs/retired/` (tracked).
   New reports go to `docs/reports/` (gitignored).
5. Markdown must pass `markdownlint-cli2`; use semantic line breaks.
6. Never use `rm`; use `trash`.
7. Do not bump the Go directive or add a `toolchain` line without an explicit request.
8. Keep `README.md`, `CLAUDE.md`, and `AGENTS.md` in step with the code and the role documents.
9. GoldenAir is the source of truth; SpaceGrey is a one-way backup target, so fixes outside a git repo do not stick there.
10. Commit and push before ending a session on SpaceGrey; an uncommitted tree is volatile there.

## Current state (2026-10-23)

- Go 1.27.0, mcp-go v1.3.0, jwt v5.3.1, dateparse pseudo-version from 2021.
- `task check` green; `go test -race` green; stdio smoke client green.
- The modernization pass is complete: toolchain bump, `go fix`, `log/slog`,
  `signal.NotifyContext` + `WaitGroup.Go`, `encoding/json/v2` for `/health`,
  `describe_time.go` rename, a single `serverVersion` const, a `cliOptions` struct,
  and refreshed documentation.
- The modernization pass was committed in `397027e`,
  including `go.sum` and `examples/test_client.go`.
- The stdio-only smoke client checks the legacy `2025-11-25` initialize handshake
  and exercises MCP `2026-07-28`
  (`server/discover`, `tools/list`, `tools/call`) with `_meta`.
  It fails on protocol or tool errors.
- The opt-in OAuth 2.1 authorization server (`internal/oauth/`, ported from Answer) landed in `bc50c03`.
  It is off unless `TIME_OAUTH_*` is configured; the static JWT remains a second path.
- Issue #1 landed in `b43a11d`: `describe_time` renders inputs that carry their own offset
  in the requested timezone.

## Roadmap

### Now

- Decide whether the agent scaffolding files should be tracked.
- Deploy with `TIME_OAUTH_*` set, forward the OAuth routes in nginx, and try a real claude.ai connector login.

### Next

- Verify the live connector login and record the result in `OPEN_ITEMS.md`.
- Land the `encodeJSON` logging fix upstream in Pushover, Alerts, and Answer.

### Later

- Replace `araddon/dateparse` with a maintained parser or a project-owned grammar.
- Add a curated `.golangci.yml` once the current default lint set is stable.

## Pro tips

- **OAuth enablement:** `bin/mcp-time -hash-password` prints the PBKDF2 hash for
  `TIME_OAUTH_LOGIN_PASSWORD_HASH`; the minimum set to turn OAuth on is
  `TIME_OAUTH_ENABLED=true`, `TIME_OAUTH_ISSUER`, `TIME_OAUTH_RESOURCE`,
  `TIME_OAUTH_LOGIN_USER`, that hash, and a 32-byte-or-longer `TIME_AUTH_SECRET_KEY`.
- **Test loop:** `task check` for the gate; `go test -race ./...` for concurrency;
  `task test-client` for the legacy initialize check
  and MCP `2026-07-28` requests over stdio.
  The smoke client needs `bin/mcp-time` first (`task build`).
- **Tool debugging:** call `describe_time` with the offending phrase and a fixed timezone; the output labels every
  ambiguity candidate and includes the machine timestamp, which is easier to assert than the prose.
- **Time parsing quirks:** `dateparse` misreads a bare `5pm` as seconds; `parseTimeComponent` normalizes it to `5:00pm`.
  Keep that normalization when touching the time path.
- **Ambiguity rules:** weekday mismatch, `mm/dd` versus `dd/mm`, and year-less dates more than 30 days in the past
  produce candidates. A single candidate renders the short form even when it carries a label.
- **mcp-go v1:** `NewTool`, `WithString`, annotation options, `CallToolRequest.GetString/RequireString`,
  and `NewToolResultText/Error` are unchanged from v0.56; the transport options used here are also unchanged.
- **HTTP auth:** a missing or invalid token is recorded in the request context and rejected by the tool middleware,
  not by the transport, so `/health` stays reachable without a token.
- **CORS:** wildcard subdomains (`*.example.com`) are supported by the custom matcher in `auth.go`;
  mcp-go's built-in CORS does not support them, which is why the handler is still ours.
- **Deployment:** the process normally runs behind nginx.
  `TIME_HTTP_DISABLE_LOCALHOST_PROTECTION` must stay `true` there or every proxied request gets a 403.

## Gap-filling context

The 2026-10-23 session started from a request to "read handoff and modernize the project", but no handoff file existed
and the role documents referenced by `AGENTS.md` had never been written.
This pass therefore did two things at once:

1. Modernized the code and toolchain (recorded in `STATUS.md`).
2. Created the missing living documents: this handoff, `ARCHITECTURE.md`, `STATUS.md`, and `OPEN_ITEMS.md`.

The modernization pass and initial role documents were committed in `397027e`.
The smoke client has since moved to MCP `2026-07-28`,
retaining one legacy initialize check.
