package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"slices"
)

const (
	modernProtocolVersion = "2026-07-28" // Removed the initialize handshake.
	legacyProtocolVersion = "2025-11-25" // Last version negotiable through initialize.
)

// MCP JSON-RPC structures
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    struct{}   `json:"capabilities"`
	ClientInfo      clientInfo `json:"clientInfo"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type requestMeta struct {
	ProtocolVersion    string     `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo         clientInfo `json:"io.modelcontextprotocol/clientInfo"`
	ClientCapabilities struct{}   `json:"io.modelcontextprotocol/clientCapabilities"`
}

type discoverParams struct {
	Meta *requestMeta `json:"_meta,omitempty"`
}

type discoverResult struct {
	SupportedVersions []string `json:"supportedVersions"`
}

type toolsListParams struct {
	Meta *requestMeta `json:"_meta,omitempty"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Meta      *requestMeta   `json:"_meta,omitempty"`
}

type toolsCallResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// main checks legacy compatibility and exercises the modern MCP protocol over stdio.
func main() {
	cmd := exec.Command("./bin/mcp-time")
	stdin, stdout := startServer(cmd)
	reader := bufio.NewReader(stdout)

	checkLegacyInitialize(stdin, reader)
	discover(stdin, reader)
	listTools(stdin, reader)
	runToolCalls(stdin, reader)

	fmt.Println("\n✅ All smoke tests passed.")
	if err := stdin.Close(); err != nil {
		log.Fatalf("close stdin: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		log.Fatalf("wait: %v", err)
	}
}

// startServer launches cmd, wiring stdin/stdout pipes and draining stderr.
func startServer(cmd *exec.Cmd) (io.WriteCloser, io.Reader) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}
	// Drain stderr (server logs). On normal shutdown the pipe closes, which
	// surfaces as ErrClosedPipe / "file already closed" — expected, not a failure.
	go func() {
		if _, err := io.Copy(os.Stderr, stderr); err != nil && !errors.Is(err, os.ErrClosed) {
			log.Printf("stderr drain: %v", err)
		}
	}()
	return stdin, stdout
}

// newRequestMeta builds the metadata required on every modern request.
func newRequestMeta() *requestMeta {
	return &requestMeta{
		ProtocolVersion: modernProtocolVersion,
		ClientInfo:      clientInfo{Name: "test-client", Version: "1.0"},
	}
}

// checkLegacyInitialize verifies the last legacy version and sends initialized.
func checkLegacyInitialize(stdin io.Writer, reader *bufio.Reader) {
	initReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustMarshal(initializeParams{
			ProtocolVersion: legacyProtocolVersion,
			ClientInfo:      clientInfo{Name: "test-client", Version: "1.0"},
		}),
	}
	send(stdin, initReq)
	resp := recv(reader)
	if resp.Error != nil {
		log.Fatalf("initialize error: %s", resp.Error.Message)
	}
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		log.Fatalf("initialize result not decoded (id=%d): %v", resp.ID, err)
	}
	if result.ProtocolVersion != legacyProtocolVersion {
		log.Fatalf("initialize protocol version: got %q, want %q", result.ProtocolVersion, legacyProtocolVersion)
	}
	fmt.Printf("[1] initialize: ok (id=%d, protocol=%s)\n", resp.ID, result.ProtocolVersion)

	send(stdin, jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
}

// discover verifies that the server supports the modern protocol version.
func discover(stdin io.Writer, reader *bufio.Reader) {
	send(stdin, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "server/discover",
		Params:  mustMarshal(discoverParams{Meta: newRequestMeta()}),
	})
	resp := recv(reader)
	if resp.Error != nil {
		log.Fatalf("server/discover error: %s", resp.Error.Message)
	}
	var result discoverResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		log.Fatalf("server/discover result not decoded (id=%d): %v", resp.ID, err)
	}
	if !slices.Contains(result.SupportedVersions, modernProtocolVersion) {
		log.Fatalf("server/discover supported versions %v do not contain %q", result.SupportedVersions, modernProtocolVersion)
	}
	fmt.Printf("[2] server/discover: ok (id=%d, protocol=%s)\n", resp.ID, modernProtocolVersion)
}

// listTools lists and prints the available tools using modern request metadata.
func listTools(stdin io.Writer, reader *bufio.Reader) {
	send(stdin, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/list",
		Params:  mustMarshal(toolsListParams{Meta: newRequestMeta()}),
	})
	resp := recv(reader)
	if resp.Error != nil {
		log.Fatalf("tools/list error: %s", resp.Error.Message)
	}
	var toolsList toolsListResult
	if err := json.Unmarshal(resp.Result, &toolsList); err != nil {
		log.Fatalf("tools/list result not decoded (id=%d): %v", resp.ID, err)
	}
	fmt.Printf("[3] tools/list: ok (id=%d)\n", resp.ID)
	for _, t := range toolsList.Tools {
		fmt.Printf("    tool: %s — %s\n", t.Name, truncate(t.Description, 80))
	}
}

// runToolCalls exercises each tool once.
func runToolCalls(stdin io.Writer, reader *bufio.Reader) {
	call(stdin, reader, 4, "get_current_time", map[string]any{"timezone": "UTC"})
	call(stdin, reader, 5, "convert_time", map[string]any{
		"source_timezone": "America/New_York",
		"time":            "09:00",
		"target_timezone": "Europe/Warsaw",
	})
	call(stdin, reader, 6, "describe_time", map[string]any{
		"date":     "tomorrow at 5pm",
		"timezone": "America/New_York",
	})
	call(stdin, reader, 7, "describe_time", map[string]any{
		"date":     "July 4th, 1776",
		"timezone": "America/New_York",
	})
	call(stdin, reader, 8, "describe_time", map[string]any{
		"date":     "Thursday, July 20th",
		"timezone": "America/New_York",
	})
}

// call invokes a tool with modern metadata and fails on protocol or tool errors.
func call(stdin io.Writer, reader *bufio.Reader, id int, tool string, args map[string]any) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  mustMarshal(toolsCallParams{Name: tool, Arguments: args, Meta: newRequestMeta()}),
	}
	send(stdin, req)
	resp := recv(reader)
	if resp.Error != nil {
		log.Fatalf("[%d] %s: ERROR — %s", id, tool, resp.Error.Message)
	}
	var cr toolsCallResult
	if err := json.Unmarshal(resp.Result, &cr); err != nil {
		log.Fatalf("[%d] %s: unmarshal error — %v", id, tool, err)
	}
	if cr.IsError {
		log.Fatalf("[%d] %s: tool error — %v", id, tool, cr.Content)
	}
	if len(cr.Content) > 0 {
		fmt.Printf("[%d] %s:\n%s\n", id, tool, indent(cr.Content[0].Text))
	}
}

// send writes a JSON-RPC request followed by a newline.
func send(w io.Writer, req jsonRPCRequest) {
	data, err := json.Marshal(req)
	if err != nil {
		log.Fatalf("marshal request: %v", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		log.Fatalf("write request: %v", err)
	}
}

// recv reads and decodes one newline-delimited JSON-RPC response.
func recv(reader *bufio.Reader) jsonRPCResponse {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		log.Fatalf("unmarshal response: %v\n%s", err, string(line))
	}
	return resp
}

// mustMarshal encodes v as JSON or fails the run.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		log.Fatalf("marshal %T: %v", v, err)
	}
	return data
}

// truncate limits s to n bytes, replacing the tail with an ellipsis.
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-3] + "..."
	}
	return s
}

// indent prefixes s with four spaces for output.
func indent(s string) string {
	return "    " + s
}
