package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestAnthropicFakeServerWireShapeAndStreamMapping(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "key" {
			t.Fatalf("expected api key header, got %q", got)
		}
		if got := r.Header.Get("x-test"); got != "yes" {
			t.Fatalf("expected default header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "text/event-stream")
		writeSSE(t, w, map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"usage": map[string]interface{}{
					"input_tokens":                10,
					"cache_read_input_tokens":     3,
					"cache_creation_input_tokens": 4,
				},
			},
		})
		writeSSE(t, w, map[string]interface{}{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]interface{}{"type": "thinking_delta", "thinking": "think"},
		})
		writeSSE(t, w, map[string]interface{}{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]interface{}{"type": "signature_delta", "signature": "sig"},
		})
		writeSSE(t, w, map[string]interface{}{
			"type":          "content_block_start",
			"index":         1,
			"content_block": map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "Read"},
		})
		writeSSE(t, w, map[string]interface{}{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": `{"file_path":"go.mod"}`},
		})
		writeSSE(t, w, map[string]interface{}{"type": "content_block_stop", "index": 1})
		writeSSE(t, w, map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]interface{}{"stop_reason": "tool_use"},
			"usage": map[string]interface{}{
				"output_tokens": 7,
			},
		})
	}))
	defer server.Close()

	provider := NewAnthropicProvider(AnthropicOptions{
		APIKey:         "key",
		BaseURL:        server.URL,
		DefaultHeaders: map[string]string{"x-test": "yes"},
	})
	max := 123
	events := provider.Stream(context.Background(), core.ProviderRequest{
		Model:           "claude-test",
		System:          []core.SystemBlock{{Type: "text", Text: "sys", Cacheable: true}},
		Tools:           []core.ToolSchema{{Name: "Read", Description: "read", InputSchema: map[string]interface{}{"type": "object"}}},
		Messages:        []core.Message{{Role: "user", Content: []core.ContentBlock{core.Text("hello")}}},
		MaxOutputTokens: &max,
		CachePrompt:     true,
		CacheTTL:        "5m",
		Effort:          "high",
	})
	var got []core.ProviderStreamEvent
	for result := range events {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		got = append(got, result.Event)
	}
	if captured["max_tokens"] != float64(123) {
		t.Fatalf("expected max_tokens 123, got %v", captured["max_tokens"])
	}
	if thinking, _ := captured["thinking"].(map[string]interface{}); thinking["budget_tokens"] != float64(8192) {
		t.Fatalf("unexpected thinking payload: %s", pretty(captured["thinking"]))
	}
	system := captured["system"].([]interface{})
	if _, ok := system[0].(map[string]interface{})["cache_control"]; !ok {
		t.Fatalf("expected cache control in system payload: %s", pretty(captured["system"]))
	}
	if got[len(got)-1].StopReason != core.StopToolUse {
		t.Fatalf("expected tool_use stop, got %s", got[len(got)-1].StopReason)
	}
	if got[len(got)-1].Usage.CacheReadTokens != 3 || got[len(got)-1].Usage.CacheCreationTokens != 4 || got[len(got)-1].Usage.OutputTokens != 7 {
		t.Fatalf("unexpected usage: %+v", got[len(got)-1].Usage)
	}
	var sawSignature, sawToolEnd bool
	for _, ev := range got {
		if ev.Type == "thinking_delta" && ev.Signature == "sig" {
			sawSignature = true
		}
		if ev.Type == "tool_use_end" && ev.ID == "toolu_1" {
			sawToolEnd = true
		}
	}
	if !sawSignature || !sawToolEnd {
		t.Fatalf("expected signature and tool end events, got %+v", got)
	}
}

func TestOpenAIChatFakeServerWireShapeAndStreamMapping(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer key" {
			t.Fatalf("expected auth header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "text/event-stream")
		writeSSE(t, w, map[string]interface{}{
			"choices": []interface{}{map[string]interface{}{"delta": map[string]interface{}{"content": "hi"}}},
		})
		writeSSE(t, w, map[string]interface{}{
			"choices": []interface{}{map[string]interface{}{
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{map[string]interface{}{
						"index": float64(0),
						"id":    "call_1",
						"function": map[string]interface{}{
							"name":      "Read",
							"arguments": `{"file_path":"go.mod"}`,
						},
					}},
				},
			}},
		})
		writeSSE(t, w, map[string]interface{}{
			"choices": []interface{}{map[string]interface{}{"finish_reason": "tool_calls"}},
			"usage":   map[string]interface{}{"prompt_tokens": 5, "completion_tokens": 6},
		})
	}))
	defer server.Close()

	provider := NewOpenAIChatCompletionsProvider(OpenAIOptions{APIKey: "key", BaseURL: server.URL})
	events := provider.Stream(context.Background(), core.ProviderRequest{
		Model: "gpt-test",
		Messages: []core.Message{{
			Role: "user",
			Content: []core.ContentBlock{
				core.Text("inspect"),
				{Type: core.BlockImage, Source: &core.ImageSource{Type: "url", URL: "https://example.test/image.png"}},
			},
		}},
		Tools: []core.ToolSchema{{Name: "Read", Description: "read", InputSchema: map[string]interface{}{"type": "object"}}},
	})
	var got []core.ProviderStreamEvent
	for result := range events {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		got = append(got, result.Event)
	}
	messages := captured["messages"].([]interface{})
	user := messages[0].(map[string]interface{})
	if _, ok := user["content"].([]interface{}); !ok {
		t.Fatalf("expected multimodal content array, got %s", pretty(user["content"]))
	}
	if got[len(got)-1].StopReason != core.StopToolUse || got[len(got)-1].Usage.InputTokens != 5 {
		t.Fatalf("unexpected final event: %+v", got[len(got)-1])
	}
}

func TestOpenAIResponsesFakeServerReplayMetadataReasoningAndStops(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "text/event-stream")
		writeSSE(t, w, map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": "resp_2"}})
		writeSSE(t, w, map[string]interface{}{
			"type":  "response.reasoning_summary_text.delta",
			"delta": "reason",
		})
		writeSSE(t, w, map[string]interface{}{
			"type": "response.output_item.added",
			"item": map[string]interface{}{"id": "item_1", "type": "function_call", "call_id": "call_1", "name": "Read"},
		})
		writeSSE(t, w, map[string]interface{}{
			"type":    "response.function_call_arguments.delta",
			"item_id": "item_1",
			"delta":   `{"file_path":"go.mod"}`,
		})
		writeSSE(t, w, map[string]interface{}{
			"type": "response.output_item.done",
			"item": map[string]interface{}{"id": "item_1", "type": "function_call", "call_id": "call_1", "name": "Read", "arguments": `{"file_path":"go.mod"}`},
		})
		writeSSE(t, w, map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     "resp_2",
				"status": "completed",
				"usage":  map[string]interface{}{"input_tokens": 2, "output_tokens": 3},
				"output": []interface{}{map[string]interface{}{"id": "item_1", "type": "function_call", "call_id": "call_1", "name": "Read"}},
			},
		})
	}))
	defer server.Close()

	provider := NewOpenAIResponsesProvider(OpenAIOptions{APIKey: "key", BaseURL: server.URL})
	events := provider.Stream(context.Background(), core.ProviderRequest{
		Model: "gpt-test",
		Messages: []core.Message{
			{
				Role:    "assistant",
				Content: []core.ContentBlock{core.Text("old")},
				ProviderMetadata: core.MessageProviderMetadata{OpenAIResponses: &core.OpenAIResponsesMetadata{
					ResponseID:  "resp_1",
					OutputItems: []map[string]interface{}{{"id": "old_item", "type": "message"}},
				}},
			},
			{Role: "user", Content: []core.ContentBlock{core.Text("next")}},
		},
		Effort: "medium",
	})
	var got []core.ProviderStreamEvent
	for result := range events {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		got = append(got, result.Event)
	}
	if captured["previous_response_id"] != "resp_1" {
		t.Fatalf("expected previous_response_id, got %v", captured["previous_response_id"])
	}
	if reasoning, _ := captured["reasoning"].(map[string]interface{}); reasoning["effort"] != "medium" {
		t.Fatalf("unexpected reasoning payload: %s", pretty(captured["reasoning"]))
	}
	if got[len(got)-1].ProviderMetadata.OpenAIResponses == nil || got[len(got)-1].ProviderMetadata.OpenAIResponses.ResponseID != "resp_2" {
		t.Fatalf("expected response metadata, got %+v", got[len(got)-1].ProviderMetadata)
	}
	if got[len(got)-1].StopReason != core.StopToolUse {
		t.Fatalf("expected tool_use stop, got %s", got[len(got)-1].StopReason)
	}
	var sawReasoning bool
	for _, ev := range got {
		if ev.Type == "thinking_delta" && ev.Text == "reason" {
			sawReasoning = true
		}
	}
	if !sawReasoning {
		t.Fatalf("expected reasoning summary event, got %+v", got)
	}
}

func TestProviderHTTPErrorMappingAndRetryAfter(t *testing.T) {
	err := providerHTTPError(http.StatusTooManyRequests, `{"error":{"message":"slow down","type":"rate_limit_error"}}`, "2")
	if err.Kind != core.ErrorRateLimit || !err.Retryable || err.RetryAfter != 2*time.Second {
		t.Fatalf("unexpected rate limit error: %+v", err)
	}
	contextErr := providerHTTPError(http.StatusBadRequest, `{"error":{"message":"maximum context length exceeded","type":"invalid_request_error"}}`, "")
	if contextErr.Kind != core.ErrorContextLength || contextErr.Retryable {
		t.Fatalf("unexpected context error: %+v", contextErr)
	}
	authErr := providerHTTPError(http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, "")
	if authErr.Kind != core.ErrorAuth || authErr.Retryable {
		t.Fatalf("unexpected auth error: %+v", authErr)
	}
}

func TestOpenAIResponsesStopMapping(t *testing.T) {
	tests := []struct {
		name     string
		resp     map[string]interface{}
		expected core.StopReason
	}{
		{name: "completed", resp: map[string]interface{}{"status": "completed"}, expected: core.StopEndTurn},
		{name: "max", resp: map[string]interface{}{"status": "incomplete", "incomplete_details": map[string]interface{}{"reason": "max_output_tokens"}}, expected: core.StopMaxTokens},
		{name: "refusal", resp: map[string]interface{}{"status": "incomplete", "incomplete_details": map[string]interface{}{"reason": "content_filter"}}, expected: core.StopRefusal},
		{name: "failed", resp: map[string]interface{}{"status": "failed"}, expected: core.StopError},
	}
	for _, tt := range tests {
		if got := mapResponsesStop(tt.resp, false); got != tt.expected {
			t.Fatalf("%s: expected %s, got %s", tt.name, tt.expected, got)
		}
	}
}

func TestResponseOutputItemsUpsertClones(t *testing.T) {
	items := upsertResponseOutputItem(nil, map[string]interface{}{"id": "item", "type": "message", "nested": map[string]interface{}{"a": "b"}})
	items = upsertResponseOutputItem(items, map[string]interface{}{"id": "item", "type": "function_call"})
	expected := []map[string]interface{}{{"id": "item", "type": "function_call"}}
	if !reflect.DeepEqual(items, expected) {
		t.Fatalf("unexpected upsert result\nexpected: %s\nactual:   %s", pretty(expected), pretty(items))
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, payload map[string]interface{}) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data: " + string(raw) + "\n\n")); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		t.Fatal(err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
