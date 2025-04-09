# TimeMCP Project Documentation

## Project Overview

TimeMCP is a server implementing the Model Control Protocol (MCP) that provides time-related tools. It serves as a utility for time zone conversions and formatting operations.

## Architecture

### Core Components

1. **MCP Server**: Uses the `github.com/mark3labs/mcp-go/server` package to implement the MCP protocol.
2. **Time Tools**: Two main tools are provided:
   - `get_current_time`: Retrieves current time in a specified timezone
   - `convert_time`: Converts times between different timezones

### Dependencies

- `github.com/mark3labs/mcp-go`: Provides the MCP protocol implementation
- `github.com/araddon/dateparse`: Flexible date parsing without requiring format specification

## Implementation Details

### Server Setup

The server is configured to use stdio for communication, making it suitable for integration with various LLM-powered applications that support the MCP protocol. It declares its capabilities during initialization, specifically enabling tool capabilities.

### Tool Handlers

#### get_current_time

This tool accepts an optional timezone parameter and returns the current time in that timezone. If no timezone is specified, it uses the system's local timezone.

Key implementation details:
- Uses `time.LoadLocation()` to get timezone information
- Formats time using a standard format string
- Returns friendly error messages for invalid timezones

#### convert_time

This tool converts a given time from one timezone to another. It has several parameters:
- `source_timezone` (optional): Defaults to system timezone
- `time` (optional): Defaults to current time
- `target_timezone` (required): The timezone to convert to

Key implementation details:
- Handles default values appropriately
- Validates timezone inputs
- Uses the dateparse library to flexibly parse time inputs
- Maintains proper timezone awareness when converting

### Error Handling

The tools are designed to provide user-friendly error messages, particularly for:
- Invalid timezone specifications
- Incorrectly formatted time strings
- Missing required parameters

### Design Decisions

1. **Using dateparse library**: Chosen for its flexibility in parsing different time formats without requiring format specification.

2. **Default values**: Both tools use reasonable defaults when parameters are not provided, making them easier to use.

3. **Stdio transport**: The server uses stdio for communication, which is the simplest approach for integration with LLM tools.

4. **Timezone validation**: The code validates timezone inputs early to provide clear error messages.

## Future Enhancements

Potential improvements for future versions:

1. **Additional time formatting options**: Allow users to specify custom output formats.

2. **More time manipulation tools**: Add capabilities for calculating time differences, adding/subtracting durations, etc.

3. **Batch operations**: Support for processing multiple times or timezones in a single request.

4. **Alternative transports**: Add support for HTTP or WebSocket in addition to stdio.

5. **Caching**: Implement caching for timezone lookups to improve performance.

## Notes on Implementation Choices

- The server uses IANA timezone database entries (e.g., "America/New_York") rather than UTC offsets (e.g., "UTC-5") because they handle daylight saving time changes automatically.

- The time format follows ISO-like conventions (YYYY-MM-DD HH:MM:SS) for better international compatibility.

- Error messages are designed to be informative and actionable, helping users correct their inputs.
