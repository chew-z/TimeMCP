---
title: TimeMCP open items
date: 2026-10-23
status: active
tags: [debt, defects, decisions]
---

# Open items

Check this list before debugging; an item may already explain the behavior.

## Repo hygiene

- **Agent scaffolding is local-only.** `AGENTS.md`, `GOLANG.md`, `GODOC.md`, `LSP.md`, `.claude/`, and `.qoder/`
  are gitignored, so a fresh clone loses the guidance map. Decide whether to track them or recreate them per machine.

## Dependencies

- **`araddon/dateparse` has no release since 2021.** It still parses correctly, but it is effectively unmaintained.
  Replacing it means reimplementing the absolute-date grammar in `flexible_parse.go`; treat that as a project, not a patch.
- **mcp-go tracks a fast-moving v1 line.** Pin deliberately and re-read the release notes on every bump.

## Code

- **No automated HTTP integration test.** CORS and JWT have unit tests, but the streamable HTTP endpoint
  and `/health` are only exercised by hand or through the smoke client (stdio only).
- **`golangci-lint` runs with default linters.** There is no `.golangci.yml`; enabling a curated set
  (errcheck, govet, staticcheck, revive) would catch more than `task lint` does today.

## Decisions already made

- **Custom CORS handler stays.** mcp-go v1 ships CORS support, but it does not cover `/health`
  and does not implement wildcard subdomains, both of which the current configuration promises.
- **DNS-rebinding protection stays disabled by default.** TimeMCP is deployed behind a reverse proxy
  that forwards the original `Host` header; enabling the guard would 403 every proxied request.
  The risk is documented in `README.md` and `CLAUDE.md`.
