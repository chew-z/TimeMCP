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
4. Documents live in `docs/`; reports go to `docs/reports/` (gitignored).
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
  a repaired `run_release.sh`, and refreshed documentation.
- The working tree contains the modernization changes; nothing was committed in that pass.
- `go.sum` (previously hidden by the global gitignore too) and `examples/test_client.go` are now un-ignored
  but still need their first commit.

## Roadmap

### Now

- Review the modernization diff, run `task check`, and commit it.
- Cut the next release with `./run_release.sh <tag>`; the script is repaired but has not been run live.
- Decide whether the agent scaffolding files should be tracked.

### Next

- Add an HTTP integration test for the streamable endpoint and `/health`.
- Refresh the smoke client to the MCP `2026-07-28` protocol revision.

### Later

- Replace `araddon/dateparse` with a maintained parser or a project-owned grammar.
- Add a curated `.golangci.yml` once the current default lint set is stable.

## Pro tips

- **Test loop:** `task check` for the gate; `go test -race ./...` for concurrency;
  `task test-client` for the real MCP handshake. The smoke client needs `bin/mcp-time` first (`task build`).
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

Nothing was committed; the next session should review the diff, run `task check`, and commit.
