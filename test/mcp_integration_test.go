package skawld_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
	"github.com/ZekromNguyen/skawld-sdk-go/tools/mcp"
)

type mcpProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
}

func (p *mcpProvider) ID() string { return "mcp-provider" }
func (p *mcpProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *mcpProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.mu.Lock()
	p.requests = append(p.requests, req)
	call := len(p.requests)
	p.mu.Unlock()
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		if call == 1 {
			out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: "call_1", Name: "mcp__echo__echo"}
			out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "call_1", JSONDelta: `{"text":"hi"}`}
			out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: "call_1"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "done"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
	}()
	return out, errs
}

func TestAgentSessionRegistersMCPToolsAndExecutesCall(t *testing.T) {
	var sawToolCall bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "application/json")
		switch req["method"] {
		case "initialize":
			id := int64(req["id"].(float64))
			writeMCPRPC(t, w, id, map[string]interface{}{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			id := int64(req["id"].(float64))
			writeMCPRPC(t, w, id, map[string]interface{}{"tools": []interface{}{map[string]interface{}{
				"name":        "echo",
				"description": "echo text",
				"inputSchema": map[string]interface{}{"type": "object", "required": []interface{}{"text"}},
			}}})
		case "tools/call":
			id := int64(req["id"].(float64))
			sawToolCall = true
			params := req["params"].(map[string]interface{})
			if params["name"] != "echo" {
				t.Fatalf("expected remote tool name echo, got %v", params["name"])
			}
			writeMCPRPC(t, w, id, map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": "echoed"}}})
		default:
			t.Fatalf("unexpected method %v", req["method"])
		}
	}))
	defer server.Close()

	provider := &mcpProvider{}
	registry := tools.NewRegistry()
	agent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider: provider,
		Model:    "fake-model",
		Tools:    registry,
		Permissions: skawld.PermissionOptions{
			Mode: skawld.PermissionModeYolo,
		},
		MCPServers: []mcp.ServerConfig{{Name: "echo", HTTP: &mcp.HTTPServerConfig{URL: server.URL}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	session, err := agent.Session(context.Background(), skawld.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sawSystemTool, sawResult bool
	for ev := range session.Run(context.Background(), "call mcp", skawld.RunOptions{}) {
		if ev.Type == skawld.EventSystem {
			for _, name := range ev.Tools {
				if name == "mcp__echo__echo" {
					sawSystemTool = true
				}
			}
		}
		if ev.Type == skawld.EventResult && ev.FinalText == "done" {
			sawResult = true
		}
	}
	if !sawSystemTool {
		t.Fatal("expected system event to include MCP tool")
	}
	if !sawToolCall {
		t.Fatal("expected MCP tool call")
	}
	if !sawResult {
		t.Fatal("expected final result")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) == 0 {
		t.Fatal("expected provider request")
	}
	var foundSchema bool
	for _, schema := range provider.requests[0].Tools {
		if schema.Name == "mcp__echo__echo" {
			foundSchema = true
		}
	}
	if !foundSchema {
		t.Fatalf("expected MCP tool schema in provider request: %+v", provider.requests[0].Tools)
	}
}

func writeMCPRPC(t *testing.T, w http.ResponseWriter, id int64, result map[string]interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Fatal(err)
	}
}
