# Release Notes

## v0.1.0 - 2025-09-12

### 🎉 New Features
- **HTTP Server & Transport:** Introduced a new HTTP server to handle requests.
- **JWT Authentication:** Implemented JWT-based authentication middleware to secure server endpoints.
- **Configurable CORS:** Added configurable Cross-Origin Resource Sharing (CORS) support for the HTTP server.
- **Timezone Configuration:** The default timezone for the application can now be configured.

### 🐛 Bug Fixes
- **Token Validation:** Improved token validation logic by using `errors.Is` for more robust error checking.

### 🔧 Improvements
- **Refactored Timezone Logic:** The logic for loading timezones has been extracted into a dedicated helper function.
- **Code Cleanup:** Removed unused files and performed general code cleanup.

### 📚 Documentation
- **Consolidated Documentation:** Consolidated and improved project documentation for clarity.
- **Updated README:** The README file was updated with better server build instructions and configuration details.
