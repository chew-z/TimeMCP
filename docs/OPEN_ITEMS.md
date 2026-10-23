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

- **`golangci-lint` runs with default linters.** There is no `.golangci.yml`; enabling a curated set
  (errcheck, govet, staticcheck, revive) would catch more than `task lint` does today.
  Five functions and one long condition in `internal/oauth` carry `//nolint:gocyclo` / `//nolint:revive`
  for exactly this reason: the package is shared byte-identically with Pushover, Alerts, and Answer,
  so it is marked rather than refactored.

## OAuth

- **No live claude.ai connector login has been tested.** The flow is covered end to end over `httptest`
  (`oauth_e2e_test.go`), but a real connector needs a deployment and nginx forwarding of
  `/.well-known/*`, `/authorize`, `/login`, `/token`, and `/register`.
- **OAuth state is in memory.** A restart drops clients, codes, and refresh tokens, so the connector
  asks for a fresh login; static `TIME_AUTH` bearer tokens are unaffected.
- **`encodeJSON` diverges from the siblings.** TimeMCP logs encode failures instead of discarding them.
  Pushover, Alerts, and Answer still use the silent `_ =`; the fix is written up in
  `/Users/rrj/Projekty/Go/src/OAUTH_ENCODEJSON_REPORT.md` and should land upstream.

## Decisions already made

- **OAuth is opt-in and the static JWT stays.** `TIME_OAUTH_ENABLED` defaults to false;
  when enabled, the MCP endpoint answers 401 with a `resource_metadata` challenge and both
  OAuth-issued and static `TIME_AUTH` tokens are accepted.
- **Custom CORS handler stays.** mcp-go v1 ships CORS support, but it does not cover `/health`
  and does not implement wildcard subdomains, both of which the current configuration promises.
- **DNS-rebinding protection stays disabled by default.** TimeMCP is deployed behind a reverse proxy
  that forwards the original `Host` header; enabling the guard would 403 every proxied request.
  The risk is documented in `README.md` and `CLAUDE.md`.
