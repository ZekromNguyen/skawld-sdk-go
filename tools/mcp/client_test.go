package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestManagerHTTPConnectRetryAndToolCall(t *testing.T) {
	var calls int32
	var sawSession bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Method == "initialize" && atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("mcp-session-id") == "sid" {
			sawSession = true
		}
		w.Header().Set("content-type", "application/json")
		w.Header().Set("mcp-session-id", "sid")
		switch req.Method {
		case "initialize":
			writeRPC(t, w, req.ID, map[string]interface{}{"protocolVersion": protocolVersion})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRPC(t, w, req.ID, map[string]interface{}{"tools": []interface{}{map[string]interface{}{
				"name":        "echo",
				"description": "echo text",
				"inputSchema": map[string]interface{}{"type": "object", "required": []interface{}{"text"}},
			}}})
		case "tools/call":
			params := req.Params.(map[string]interface{})
			args := params["arguments"].(map[string]interface{})
			writeRPC(t, w, req.ID, map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": args["text"]}}})
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	manager := NewManager([]ServerConfig{{Name: "srv", HTTP: &HTTPServerConfig{URL: server.URL}}})
	if _, err := manager.Connect(context.Background(), nil); err == nil {
		t.Fatal("expected first connection to fail")
	}
	tools, err := manager.Connect(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name() != "mcp__srv__echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	input, err := tools[0].Validate(map[string]interface{}{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tools[0].Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "hello" {
		t.Fatalf("unexpected tool result: %+v", res)
	}
	if !sawSession {
		t.Fatal("expected session id to be replayed after initialize")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPTransportDecodesSSEResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "text/event-stream")
		raw, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"ok": true}})
		_, _ = w.Write([]byte("event: message\n"))
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	}))
	defer server.Close()

	tr := newHTTPTransport(HTTPServerConfig{URL: server.URL})
	result, err := tr.Request(context.Background(), rpcRequest{JSONRPC: "2.0", ID: 1, Method: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestStdioEchoMCPServerEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("exec unavailable")
	}
	dir := t.TempDir()
	serverPath := filepath.Join(dir, "echo_mcp.go")
	if err := os.WriteFile(serverPath, []byte(echoMCPServerSource), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager([]ServerConfig{{Name: "echo server", Stdio: &StdioServerConfig{Command: "go", Args: []string{"run", serverPath}}}})
	tools, err := manager.Connect(context.Background(), []string{"mcp__echo_server__echo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name() != "mcp__echo_server__echo_2" {
		t.Fatalf("unexpected tool names after collision handling: %+v", toolNames(tools))
	}
	input, err := tools[0].Validate(map[string]interface{}{"text": "stdio ok"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tools[0].Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content.(string), "stdio ok") {
		t.Fatalf("unexpected stdio result: %+v", res)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func toolNames(tools []core.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name())
	}
	return out
}

func writeRPC(t *testing.T, w http.ResponseWriter, id int64, result map[string]interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result}); err != nil {
		t.Fatal(err)
	}
}

const echoMCPServerSource = `package main

import (
	"bufio"
	"encoding/json"
	"os"
)

type req struct {
	JSONRPC string                 ` + "`json:\"jsonrpc\"`" + `
	ID      int64                  ` + "`json:\"id,omitempty\"`" + `
	Method  string                 ` + "`json:\"method\"`" + `
	Params  map[string]interface{} ` + "`json:\"params,omitempty\"`" + `
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var r req
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.ID == 0 {
			continue
		}
		switch r.Method {
		case "initialize":
			enc.Encode(map[string]interface{}{"jsonrpc":"2.0","id":r.ID,"result":map[string]interface{}{"protocolVersion":"2025-06-18"}})
		case "tools/list":
			enc.Encode(map[string]interface{}{"jsonrpc":"2.0","id":r.ID,"result":map[string]interface{}{"tools":[]interface{}{map[string]interface{}{"name":"echo","description":"echo text","inputSchema":map[string]interface{}{"type":"object","required":[]interface{}{"text"}}}}}})
		case "tools/call":
			params, _ := r.Params["arguments"].(map[string]interface{})
			text, _ := params["text"].(string)
			enc.Encode(map[string]interface{}{"jsonrpc":"2.0","id":r.ID,"result":map[string]interface{}{"content":[]interface{}{map[string]interface{}{"type":"text","text":text}}}})
		}
	}
}
`
