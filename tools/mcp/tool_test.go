package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestConvertToolResultTextImageAndStructured(t *testing.T) {
	text := ConvertToolResult(map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "text", "text": "world"},
		},
	})
	if text.Content != "hello\nworld" || text.Summary != "hello\nworld" || text.IsError {
		t.Fatalf("unexpected text result: %+v", text)
	}
	image := ConvertToolResult(map[string]interface{}{
		"isError": true,
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "see"},
			map[string]interface{}{"type": "image", "mimeType": "image/png", "data": "raw"},
		},
	})
	blocks, ok := image.Content.([]core.ContentBlock)
	if !ok || len(blocks) != 2 || blocks[1].Source == nil || blocks[1].Source.MediaType != "image/png" {
		t.Fatalf("unexpected image result: %+v", image)
	}
	if !image.IsError {
		t.Fatal("expected error result")
	}
	structured := ConvertToolResult(map[string]interface{}{"structuredContent": map[string]interface{}{"ok": true}})
	if structured.Content != `{"ok":true}` {
		t.Fatalf("unexpected structured result: %+v", structured)
	}
}

func TestMCPToolValidateRequiredFields(t *testing.T) {
	tool := &Tool{displayName: "mcp__s__echo", remote: RemoteTool{
		Name: "echo",
		InputSchema: map[string]interface{}{
			"type":     "object",
			"required": []interface{}{"text"},
		},
	}}
	if _, err := tool.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected required field validation error")
	}
	input, err := tool.Validate(map[string]interface{}{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if input["text"] != "hello" {
		t.Fatalf("unexpected input: %+v", input)
	}
}

func TestClientCallToolUsesRemoteName(t *testing.T) {
	tr := &fakeTransport{results: []map[string]interface{}{
		{"content": []interface{}{map[string]interface{}{"type": "text", "text": "ok"}}},
	}}
	client := &Client{transport: tr}
	tool := &Tool{displayName: "mcp__s__renamed", serverName: "s", remote: RemoteTool{Name: "remote"}, client: client}
	observer := &toolObserver{}
	res, err := tool.Execute(map[string]interface{}{"x": "y"}, core.ToolContext{
		Context:   context.Background(),
		Observer:  observer,
		SessionID: "session_1",
		RunID:     "run_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := tr.requests[0].Params.(map[string]interface{})["name"]; got != "remote" {
		t.Fatalf("expected remote tool name, got %v", got)
	}
	if len(observer.observations) != 1 {
		t.Fatalf("expected one observation, got %+v", observer.observations)
	}
	observation := observer.observations[0]
	if observation.Type != core.ObservationMCPCall || observation.SessionID != "session_1" || observation.RunID != "run_1" || observation.ToolName != "mcp__s__renamed" {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestClientCallToolWrapsTransportError(t *testing.T) {
	sentinel := errors.New("transport failed")
	client := &Client{transport: &fakeTransport{err: sentinel}}
	_, err := client.CallTool(context.Background(), "remote", map[string]interface{}{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped transport error, got %v", err)
	}
}

type fakeTransport struct {
	requests []rpcRequest
	results  []map[string]interface{}
	err      error
}

func (t *fakeTransport) Request(ctx context.Context, req rpcRequest) (map[string]interface{}, error) {
	t.requests = append(t.requests, req)
	if t.err != nil {
		return nil, t.err
	}
	result := map[string]interface{}{}
	if len(t.results) > 0 {
		result = t.results[0]
		t.results = t.results[1:]
	}
	return result, nil
}

func (t *fakeTransport) Notify(ctx context.Context, req rpcRequest) error {
	t.requests = append(t.requests, req)
	return nil
}

func (t *fakeTransport) Close() error { return nil }

type toolObserver struct {
	observations []core.Observation
}

func (o *toolObserver) Observe(ctx context.Context, observation core.Observation) {
	o.observations = append(o.observations, observation)
}
