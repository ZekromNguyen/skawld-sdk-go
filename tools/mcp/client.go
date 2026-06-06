package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/skawld/skawld-sdk-go/core"
	internalsse "github.com/skawld/skawld-sdk-go/internal/sse"
)

type Manager struct {
	configs   []ServerConfig
	mu        sync.Mutex
	connected bool
	clients   []*Client
	tools     []core.Tool
}

func NewManager(configs []ServerConfig) *Manager {
	cp := append([]ServerConfig(nil), configs...)
	return &Manager{configs: cp}
}

func (m *Manager) Connect(ctx context.Context, existingNames []string) ([]core.Tool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.connected {
		return nil, nil
	}
	used := map[string]struct{}{}
	for _, name := range existingNames {
		used[name] = struct{}{}
	}
	var clients []*Client
	var out []core.Tool
	for _, cfg := range m.configs {
		if cfg.Disabled {
			continue
		}
		if err := cfg.Validate(); err != nil {
			closeClients(clients)
			return nil, err
		}
		client, err := openClient(ctx, cfg)
		if err != nil {
			closeClients(clients)
			return nil, err
		}
		clients = append(clients, client)
		remoteTools, err := client.ListTools(ctx)
		if err != nil {
			closeClients(clients)
			return nil, err
		}
		for _, remote := range remoteTools {
			name := UniqueToolName(cfg.Name, remote.Name, used)
			out = append(out, &Tool{displayName: name, serverName: cfg.Name, remote: remote, client: client})
		}
	}
	m.clients = clients
	m.tools = out
	m.connected = true
	return append([]core.Tool(nil), out...), nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []string
	for _, client := range m.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	m.clients = nil
	m.connected = false
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}
	return nil
}

func closeClients(clients []*Client) {
	for _, client := range clients {
		_ = client.Close()
	}
}

type Client struct {
	name      string
	transport transport
	nextID    atomic.Int64
}

type transport interface {
	Request(ctx context.Context, req rpcRequest) (map[string]interface{}, error)
	Notify(ctx context.Context, req rpcRequest) error
	Close() error
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int64                  `json:"id,omitempty"`
	Result  map[string]interface{} `json:"result,omitempty"`
	Error   *rpcError              `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type RemoteTool struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
}

func openClient(ctx context.Context, cfg ServerConfig) (*Client, error) {
	var tr transport
	var err error
	if cfg.Stdio != nil {
		tr, err = openStdioTransport(cfg)
	} else {
		tr = newHTTPTransport(*cfg.HTTP)
	}
	if err != nil {
		return nil, err
	}
	client := &Client{name: cfg.Name, transport: tr}
	if err := client.Initialize(ctx); err != nil {
		_ = tr.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", map[string]interface{}{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "skawld-sdk-go", "version": "0.1.0"},
	})
	if err != nil {
		return err
	}
	return c.notify(ctx, "notifications/initialized", map[string]interface{}{})
}

func (c *Client) ListTools(ctx context.Context) ([]RemoteTool, error) {
	result, err := c.request(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	rawTools, _ := result["tools"].([]interface{})
	tools := make([]RemoteTool, 0, len(rawTools))
	for _, raw := range rawTools {
		obj, _ := raw.(map[string]interface{})
		name, _ := obj["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		desc, _ := obj["description"].(string)
		schema, _ := obj["inputSchema"].(map[string]interface{})
		if schema == nil {
			schema, _ = obj["input_schema"].(map[string]interface{})
		}
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		tools = append(tools, RemoteTool{Name: name, Description: desc, InputSchema: schema})
	}
	return tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (core.ToolResult, error) {
	result, err := c.request(ctx, "tools/call", map[string]interface{}{"name": name, "arguments": args})
	if err != nil {
		return core.ToolResult{}, err
	}
	return ConvertToolResult(result), nil
}

func (c *Client) Close() error {
	return c.transport.Close()
}

func (c *Client) request(ctx context.Context, method string, params interface{}) (map[string]interface{}, error) {
	id := c.nextID.Add(1)
	return c.transport.Request(ctx, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
}

func (c *Client) notify(ctx context.Context, method string, params interface{}) error {
	return c.transport.Notify(ctx, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

type stdioTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[int64]chan rpcResult
	readDone  chan struct{}
	closeOnce sync.Once
	waitOnce  sync.Once
	waitErr   error
}

type rpcResult struct {
	resp rpcResponse
	err  error
}

func openStdioTransport(cfg ServerConfig) (*stdioTransport, error) {
	cmd := exec.Command(cfg.Stdio.Command, cfg.Stdio.Args...)
	if cfg.Stdio.CWD != "" {
		cmd.Dir = cfg.Stdio.CWD
	}
	cmd.Env = os.Environ()
	for k, v := range cfg.Stdio.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go io.Copy(io.Discard, stderr)
	tr := &stdioTransport{
		cmd:      cmd,
		stdin:    stdin,
		pending:  map[int64]chan rpcResult{},
		readDone: make(chan struct{}),
	}
	go tr.readLoop(stdout)
	return tr, nil
}

func (t *stdioTransport) Request(ctx context.Context, req rpcRequest) (map[string]interface{}, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch := t.register(req.ID)
	defer t.unregister(req.ID)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.writeMu.Lock()
	if _, err := t.stdin.Write(append(raw, '\n')); err != nil {
		t.writeMu.Unlock()
		return nil, err
	}
	t.writeMu.Unlock()
	select {
	case result := <-ch:
		if result.err != nil {
			return nil, result.err
		}
		return responseResult(result.resp, req.ID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *stdioTransport) Notify(ctx context.Context, req rpcRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.stdin.Write(append(raw, '\n'))
	return err
}

func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		t.failPending(io.ErrClosedPipe)
		_ = t.wait()
	})
	return nil
}

func (t *stdioTransport) register(id int64) <-chan rpcResult {
	ch := make(chan rpcResult, 1)
	t.pendingMu.Lock()
	t.pending[id] = ch
	t.pendingMu.Unlock()
	return ch
}

func (t *stdioTransport) unregister(id int64) {
	t.pendingMu.Lock()
	delete(t.pending, id)
	t.pendingMu.Unlock()
}

func (t *stdioTransport) readLoop(stdout io.Reader) {
	defer close(t.readDone)
	decoder := json.NewDecoder(stdout)
	for {
		var resp rpcResponse
		if err := decoder.Decode(&resp); err != nil {
			t.failPending(err)
			return
		}
		t.pendingMu.Lock()
		ch := t.pending[resp.ID]
		if ch != nil {
			delete(t.pending, resp.ID)
		}
		t.pendingMu.Unlock()
		if ch != nil {
			ch <- rpcResult{resp: resp}
		}
	}
}

func (t *stdioTransport) failPending(err error) {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	for id, ch := range t.pending {
		ch <- rpcResult{err: err}
		delete(t.pending, id)
	}
}

func (t *stdioTransport) wait() error {
	t.waitOnce.Do(func() {
		if t.cmd != nil {
			t.waitErr = t.cmd.Wait()
		}
	})
	return t.waitErr
}

type httpTransport struct {
	client           *http.Client
	endpoint         string
	headers          map[string]string
	maxSSEEventBytes int
	mu               sync.RWMutex
	sessionID        string
}

var defaultMCPHTTPClient = &http.Client{}

func newHTTPTransport(cfg HTTPServerConfig) *httpTransport {
	headers := make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		headers[k] = v
	}
	client := cfg.HTTPClient
	if client == nil {
		client = defaultMCPHTTPClient
	}
	if cfg.Timeout > 0 {
		copy := *client
		copy.Timeout = cfg.Timeout
		client = &copy
	}
	return &httpTransport{client: client, endpoint: cfg.URL, headers: headers, maxSSEEventBytes: cfg.MaxSSEEventBytes}
}

func (t *httpTransport) Request(ctx context.Context, req rpcRequest) (map[string]interface{}, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "application/json, text/event-stream")
	if sessionID := t.currentSessionID(); sessionID != "" {
		httpReq.Header.Set("mcp-session-id", sessionID)
	}
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	t.updateSessionID(resp.Header.Get("mcp-session-id"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	ct := resp.Header.Get("content-type")
	if strings.Contains(ct, "text/event-stream") {
		return decodeSSEResponse(ctx, resp.Body, req.ID, t.maxSSEEventBytes)
	}
	var rpc rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return nil, err
	}
	return responseResult(rpc, req.ID)
}

func (t *httpTransport) Notify(ctx context.Context, req rpcRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("content-type", "application/json")
	if sessionID := t.currentSessionID(); sessionID != "" {
		httpReq.Header.Set("mcp-session-id", sessionID)
	}
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	t.updateSessionID(resp.Header.Get("mcp-session-id"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mcp http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (t *httpTransport) Close() error { return nil }

func (t *httpTransport) currentSessionID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sessionID
}

func (t *httpTransport) updateSessionID(sessionID string) {
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	t.sessionID = sessionID
	t.mu.Unlock()
}

func decodeSSEResponse(ctx context.Context, r io.Reader, id int64, maxEventBytes int) (map[string]interface{}, error) {
	parser := internalsse.NewParser(r, maxEventBytes)
	for {
		event, ok, err := parser.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if result, ok, err := maybeDecodeSSEData(event.Data, id); ok || err != nil {
			return result, err
		}
	}
	return nil, fmt.Errorf("mcp sse response for id %d not found", id)
}

func maybeDecodeSSEData(data string, id int64) (map[string]interface{}, bool, error) {
	if strings.TrimSpace(data) == "" {
		return nil, false, nil
	}
	var rpc rpcResponse
	if err := json.Unmarshal([]byte(data), &rpc); err != nil {
		return nil, false, err
	}
	if rpc.ID != id {
		return nil, false, nil
	}
	result, err := responseResult(rpc, id)
	return result, true, err
}

func responseResult(resp rpcResponse, id int64) (map[string]interface{}, error) {
	if resp.ID != id {
		return nil, fmt.Errorf("mcp response id mismatch: expected %d got %d", id, resp.ID)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		return map[string]interface{}{}, nil
	}
	return resp.Result, nil
}
