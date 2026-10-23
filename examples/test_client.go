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
)

// MCP JSON-RPC structures
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
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
	ProtocolVersion string   `json:"protocolVersion"`
	Capabilities    struct{} `json:"capabilities"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
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
}

type toolsCallResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func main() {
	cmd := exec.Command("./bin/mcp-time")
	stdin, stdout := startServer(cmd)
	reader := bufio.NewReader(stdout)

	handshake(stdin, reader)
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

// handshake performs the MCP initialize / initialized / tools/list exchange.
func handshake(stdin io.Writer, reader *bufio.Reader) {
	initReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustMarshal(initializeParams{
			ProtocolVersion: "2024-11-05",
			ClientInfo: struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}{Name: "test-client", Version: "1.0"},
		}),
	}
	send(stdin, initReq)
	resp := recv(reader)
	fmt.Printf("[1] initialize: ok (id=%d)\n", resp.ID)
	if resp.Error != nil {
		log.Fatalf("initialize error: %s", resp.Error.Message)
	}

	send(stdin, jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

	send(stdin, jsonRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	resp = recv(reader)
	fmt.Printf("[2] tools/list: ok (id=%d)\n", resp.ID)
	var toolsList toolsListResult
	if err := json.Unmarshal(resp.Result, &toolsList); err != nil {
		log.Printf("tools/list result not decoded (id=%d): %v", resp.ID, err)
	}
	for _, t := range toolsList.Tools {
		fmt.Printf("    tool: %s — %s\n", t.Name, truncate(t.Description, 80))
	}
}

// runToolCalls exercises each tool once.
func runToolCalls(stdin io.Writer, reader *bufio.Reader) {
	call(stdin, reader, 3, "get_current_time", map[string]any{"timezone": "UTC"})
	call(stdin, reader, 4, "convert_time", map[string]any{
		"source_timezone": "America/New_York",
		"time":            "09:00",
		"target_timezone": "Europe/Warsaw",
	})
	call(stdin, reader, 5, "describe_time", map[string]any{
		"date":     "tomorrow at 5pm",
		"timezone": "America/New_York",
	})
	call(stdin, reader, 6, "describe_time", map[string]any{
		"date":     "July 4th, 1776",
		"timezone": "America/New_York",
	})
	call(stdin, reader, 7, "describe_time", map[string]any{
		"date":     "Thursday, July 20th",
		"timezone": "America/New_York",
	})
}

func call(stdin io.Writer, reader *bufio.Reader, id int, tool string, args map[string]any) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  mustMarshal(toolsCallParams{Name: tool, Arguments: args}),
	}
	send(stdin, req)
	resp := recv(reader)
	if resp.Error != nil {
		fmt.Printf("[%d] %s: ERROR — %s\n", id, tool, resp.Error.Message)
		return
	}
	var cr toolsCallResult
	if err := json.Unmarshal(resp.Result, &cr); err != nil {
		fmt.Printf("[%d] %s: unmarshal error — %v\n", id, tool, err)
		return
	}
	if len(cr.Content) > 0 {
		fmt.Printf("[%d] %s:\n%s\n", id, tool, indent(cr.Content[0].Text))
	}
}

func send(w io.Writer, req jsonRPCRequest) {
	data, err := json.Marshal(req)
	if err != nil {
		log.Fatalf("marshal request: %v", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		log.Fatalf("write request: %v", err)
	}
}

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

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		log.Fatalf("marshal %T: %v", v, err)
	}
	return data
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-3] + "..."
	}
	return s
}

func indent(s string) string {
	return "    " + s
}
