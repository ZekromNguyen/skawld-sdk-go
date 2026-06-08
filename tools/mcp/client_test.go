package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestHTTPTransportConcurrentRequestsUseUniqueIDsAndSessionHeader(t *testing.T) {
	var sawSession int32
	ids := sync.Map{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if _, loaded := ids.LoadOrStore(req.ID, true); loaded {
			t.Fatalf("duplicate request id %d", req.ID)
		}
		if r.Header.Get("mcp-session-id") == "sid" {
			atomic.AddInt32(&sawSession, 1)
		}
		w.Header().Set("content-type", "application/json")
		w.Header().Set("mcp-session-id", "sid")
		writeRPC(t, w, req.ID, map[string]interface{}{"id": req.ID})
	}))
	defer server.Close()

	tr := newHTTPTransport(HTTPServerConfig{URL: server.URL})
	client := &Client{name: "test", transport: tr}
	if _, err := client.request(context.Background(), "warmup", nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.request(context.Background(), "tools/call", nil); err != nil {
				t.Errorf("request failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&sawSession) == 0 {
		t.Fatal("expected concurrent requests to use stored session header")
	}
	var count int
	ids.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	if count != 21 {
		t.Fatalf("expected 21 unique request ids, got %d", count)
	}
}

func TestHTTPTransportUsesCustomClient(t *testing.T) {
	var calls int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		var rpc rpcRequest
		if err := json.NewDecoder(req.Body).Decode(&rpc); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]interface{}{"ok": true}})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"content-type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    req,
		}, nil
	})}
	tr := newHTTPTransport(HTTPServerConfig{URL: "https://mcp.test", HTTPClient: client})
	result, err := tr.Request(context.Background(), rpcRequest{JSONRPC: "2.0", ID: 1, Method: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("unexpected result=%+v calls=%d", result, calls)
	}
}

type closeErrorTransport struct {
	err error
}

func (t closeErrorTransport) Request(ctx context.Context, req rpcRequest) (map[string]interface{}, error) {
	return nil, nil
}
func (t closeErrorTransport) Notify(ctx context.Context, req rpcRequest) error { return nil }
func (t closeErrorTransport) Close() error                                     { return t.err }

func TestManagerCloseJoinsClientErrors(t *testing.T) {
	errA := errors.New("close a")
	errB := errors.New("close b")
	manager := &Manager{
		connected: true,
		clients: []*Client{
			{name: "a", transport: closeErrorTransport{err: errA}},
			{name: "b", transport: closeErrorTransport{err: errB}},
		},
	}
	err := manager.Close()
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("expected joined close errors, got %v", err)
	}
}

func TestStdioRequestCancellationDoesNotBlockDecode(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("exec unavailable")
	}
	dir := t.TempDir()
	serverPath := filepath.Join(dir, "blocking_mcp.go")
	if err := os.WriteFile(serverPath, []byte(blockingMCPServerSource), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := openStdioTransport(ServerConfig{Name: "blocking", Stdio: &StdioServerConfig{Command: "go", Args: []string{"run", serverPath}}})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = tr.Request(ctx, rpcRequest{JSONRPC: "2.0", ID: 1, Method: "never"})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if time.Since(started) > time.Second {
		t.Fatal("stdio request did not return promptly after cancellation")
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

const blockingMCPServerSource = `package main

import (
	"bufio"
	"os"
	"time"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		time.Sleep(time.Hour)
	}
}
`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
