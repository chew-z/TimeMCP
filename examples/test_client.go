package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"
)

// Simple structures for JSON-RPC requests and responses
type JSONRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int                    `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}

type JSONRPCResponse struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int                    `json:"id"`
	Result  map[string]interface{} `json:"result,omitempty"`
	Error   map[string]interface{} `json:"error,omitempty"`
}

func main() {
	// Start the MCP server as a subprocess
	cmd := exec.Command("go", "run", "../main.go")
	cmd.Stderr = os.Stderr

	// Set up pipes for stdin and stdout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("Failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to get stdout pipe: %v", err)
	}

	// Start the server
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	defer cmd.Process.Kill()

	// Initialize the server with a request
	initRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "TestClient",
				"version": "1.0.0",
			},
		},
	}

	// Send initialization request
	if err := json.NewEncoder(stdin).Encode(initRequest); err != nil {
		log.Fatalf("Failed to encode init request: %v", err)
	}

	// Read the response
	var initResponse JSONRPCResponse
	if err := json.NewDecoder(stdout).Decode(&initResponse); err != nil {
		log.Fatalf("Failed to decode init response: %v", err)
	}
	fmt.Println("Server initialized:")
	prettyPrint(initResponse)

	// Send initialized notification
	initializedNotification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialized",
		"params":  map[string]interface{}{},
	}
	if err := json.NewEncoder(stdin).Encode(initializedNotification); err != nil {
		log.Fatalf("Failed to encode initialized notification: %v", err)
	}

	// Test get_current_time tool
	getTimeRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "get_current_time",
			"arguments": map[string]interface{}{
				"timezone": "America/New_York",
			},
		},
	}
	if err := json.NewEncoder(stdin).Encode(getTimeRequest); err != nil {
		log.Fatalf("Failed to encode get_time request: %v", err)
	}

	// Read the response
	var getTimeResponse JSONRPCResponse
	if err := json.NewDecoder(stdout).Decode(&getTimeResponse); err != nil {
		log.Fatalf("Failed to decode get_time response: %v", err)
	}
	fmt.Println("\nGet Current Time Result:")
	prettyPrint(getTimeResponse)

	// Test convert_time tool
	convertTimeRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "convert_time",
			"arguments": map[string]interface{}{
				"source_timezone": "Europe/Warsaw",
				"time":            time.Now().Format("15:04"),
				"target_timezone": "Asia/Tokyo",
			},
		},
	}
	if err := json.NewEncoder(stdin).Encode(convertTimeRequest); err != nil {
		log.Fatalf("Failed to encode convert_time request: %v", err)
	}

	// Read the response
	var convertTimeResponse JSONRPCResponse
	if err := json.NewDecoder(stdout).Decode(&convertTimeResponse); err != nil {
		log.Fatalf("Failed to decode convert_time response: %v", err)
	}
	fmt.Println("\nConvert Time Result:")
	prettyPrint(convertTimeResponse)

	// Allow time for cleanup
	time.Sleep(100 * time.Millisecond)
}

// Helper function to pretty-print JSON
func prettyPrint(v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println("Error:", err)
	}
	fmt.Println(string(b))
}
