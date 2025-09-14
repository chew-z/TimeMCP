# Release Notes

## v0.2.0 - 2025-09-14

### 🎉 New Features
- **HTTP Transport:** The server can now be run with an HTTP transport (`--transport=http`), providing RESTful endpoints for health (`/health`), capabilities (`/capabilities`), and MCP (`/mcp`).
- **JWT Authentication:** Implemented JWT-based authentication for the HTTP transport, which can be enabled with `--auth-enabled`. This includes a utility to generate tokens (`--generate-token`).
- **Configurable CORS:** Added CORS support for the HTTP transport, configurable via environment variables (`TIME_HTTP_CORS_ENABLED`, `TIME_HTTP_CORS_ORIGINS`).

### 🔧 Improvements
- **Enhanced CORS Security:** The CORS configuration has been hardened. It is now disabled by default, and when enabled, requires an explicit allowlist of origins. Using a wildcard (`*`) origin with authentication enabled is now rejected at startup.
- **Improved Error Propagation:** The HTTP server now correctly propagates startup errors, preventing silent failures.
- **Default Timezone Configuration:** The default timezone for operations can now be configured using the `TIME_DEFAULT_TIMEZONE` environment variable.

### 🐛 Bug Fixes
- **Token Validation:** The JWT validation now correctly uses `errors.Is` to check for expired tokens.

### 📚 Documentation
- The `README.md` and `CLAUDE.md` files have been significantly updated to reflect the new HTTP transport, authentication, and CORS features.

### ⚙️ Internal Changes
- Added new tests for the hardened CORS implementation.
- Refactored timezone loading logic into a helper function.