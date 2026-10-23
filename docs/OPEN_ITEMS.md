---
title: TimeMCP open items
date: 2026-10-23
status: active
tags: [debt, defects, decisions]
---

# Open items

Check this list before debugging; an item may already explain the behavior.

## Repo hygiene

- **`go.sum` was untracked.** Both the project `.gitignore` and the global `~/.gitignore` ignored it;
  the project now overrides that with `!go.sum`.
  The file still needs to be added and committed so builds are reproducible on another machine.
- **`examples/test_client.go` was untracked.** The `test_*` ignore pattern matched it accidentally.
  The pattern is now anchored to the repo root as `/test_*`; the example still needs to be committed.
- **Agent scaffolding is local-only.** `AGENTS.md`, `GOLANG.md`, `GODOC.md`, `LSP.md`, `.claude/`, and `.qoder/`
  are gitignored, so a fresh clone loses the guidance map. Decide whether to track them or recreate them per machine.
- **`run_release.sh` is untracked and untested live.** It was repaired on 2026-10-23 to take a tag argument,
  build `bin/mcp-time`, and generate release notes; `bash -n` and `shellcheck` pass, but no release has been cut with it.

## Dependencies

- **`araddon/dateparse` has no release since 2021.** It still parses correctly, but it is effectively unmaintained.
  Replacing it means reimplementing the absolute-date grammar in `flexible_parse.go`; treat that as a project, not a patch.
- **mcp-go tracks a fast-moving v1 line.** Pin deliberately and re-read the release notes on every bump.

## Code

- **No automated HTTP integration test.** CORS and JWT have unit tests, but the streamable HTTP endpoint
  and `/health` are only exercised by hand or through the smoke client (stdio only).
- **The smoke client declares MCP `2024-11-05`.** It works with mcp-go v1.3.0 but does not exercise the
  `2026-07-28` revision the library now supports.
- **`golangci-lint` runs with default linters.** There is no `.golangci.yml`; enabling a curated set
  (errcheck, govet, staticcheck, revive) would catch more than `task lint` does today.

## Decisions already made

- **Custom CORS handler stays.** mcp-go v1 ships CORS support, but it does not cover `/health`
  and does not implement wildcard subdomains, both of which the current configuration promises.
- **DNS-rebinding protection stays disabled by default.** TimeMCP is deployed behind a reverse proxy
  that forwards the original `Host` header; enabling the guard would 403 every proxied request.
  The risk is documented in `README.md` and `CLAUDE.md`.
