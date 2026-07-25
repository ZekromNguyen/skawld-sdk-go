package providers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestAnthropicTranslationIncludesSystemToolsAndContentBlocks(t *testing.T) {
	system := anthropicSystem([]core.SystemBlock{
		{Type: "text", Text: "base"},
		{Type: "text", Text: "cached", Cacheable: true},
	}, true, "5m")
	expectedSystem := []map[string]interface{}{
		{"type": "text", "text": "base"},
		{"type": "text", "text": "cached", "cache_control": map[string]interface{}{"type": "ephemeral", "ttl": "5m"}},
	}
	if !reflect.DeepEqual(system, expectedSystem) {
		t.Fatalf("unexpected system translation\nexpected: %s\nactual:   %s", pretty(expectedSystem), pretty(system))
	}

	tools := anthropicTools([]core.ToolSchema{{
		Name:        "Read",
		Description: "read a file",
		InputSchema: map[string]interface{}{"type": "object"},
	}}, true, "5m")
	expectedTools := []map[string]interface{}{{
		"name":         "Read",
		"description":  "read a file",
		"input_schema": map[string]interface{}{"type": "object"},
		"cache_control": map[string]interface{}{
			"type": "ephemeral",
			"ttl":  "5m",
		},
	}}
	if !reflect.DeepEqual(tools, expectedTools) {
		t.Fatalf("unexpected tools translation\nexpected: %s\nactual:   %s", pretty(expectedTools), pretty(tools))
	}

	messages := anthropicMessages([]core.Message{
		{
			Role: "user",
			Content: []core.ContentBlock{
				core.Text("hello"),
				{Type: core.BlockImage, Source: &core.ImageSource{Type: "base64", MediaType: "image/png", Data: "abc"}},
			},
		},
		{
			Role: "assistant",
			Content: []core.ContentBlock{
				{Type: core.BlockThinking, Thinking: "consider", Signature: "sig"},
				core.ToolUse("toolu_1", "Read", map[string]interface{}{"file_path": "go.mod"}),
			},
		},
		{
			Role: "user",
			Content: []core.ContentBlock{
				core.ToolResultBlock("toolu_1", []interface{}{"ok"}, true),
			},
		},
	}, false, "")
	expectedMessages := []map[string]interface{}{
		{
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "text", "text": "hello"},
				{"type": "image", "source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": "abc"}},
			},
		},
		{
			"role": "assistant",
			"content": []map[string]interface{}{
				{"type": "thinking", "thinking": "consider", "signature": "sig"},
				{"type": "tool_use", "id": "toolu_1", "name": "Read", "input": map[string]interface{}{"file_path": "go.mod"}},
			},
		},
		{
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": `["ok"]`, "is_error": true},
			},
		},
	}
	if !reflect.DeepEqual(messages, expectedMessages) {
		t.Fatalf("unexpected message translation\nexpected: %s\nactual:   %s", pretty(expectedMessages), pretty(messages))
	}
}

func TestOpenAIChatTranslationIncludesSystemToolCallsAndResults(t *testing.T) {
	provider := NewOpenAIChatCompletionsProvider(OpenAIOptions{})
	messages := provider.translateMessages(core.ProviderRequest{
		System: []core.SystemBlock{{Type: "text", Text: "base"}, {Type: "text", Text: "rules"}},
		Messages: []core.Message{
			{Role: "user", Content: []core.ContentBlock{core.Text("hello")}},
			{
				Role: "assistant",
				Content: []core.ContentBlock{
					core.Text("checking"),
					core.ToolUse("call_1", "Read", map[string]interface{}{"file_path": "go.mod"}),
				},
			},
			{Role: "user", Content: []core.ContentBlock{core.ToolResultBlock("call_1", map[string]interface{}{"ok": true}, false)}},
		},
	})
	expected := []map[string]interface{}{
		{"role": "system", "content": "base\n\nrules"},
		{"role": "user", "content": "hello"},
		{
			"role":    "assistant",
			"content": "checking",
			"tool_calls": []map[string]interface{}{{
				"id":   "call_1",
				"type": "function",
				"function": map[string]interface{}{
					"name":      "Read",
					"arguments": `{"file_path":"go.mod"}`,
				},
			}},
		},
		{"role": "tool", "tool_call_id": "call_1", "content": `{"ok":true}`},
	}
	if !reflect.DeepEqual(messages, expected) {
		t.Fatalf("unexpected chat translation\nexpected: %s\nactual:   %s", pretty(expected), pretty(messages))
	}
}

func TestOpenAIChatTranslationIncludesImagesAndToolResultFallbacks(t *testing.T) {
	provider := NewOpenAIChatCompletionsProvider(OpenAIOptions{})
	messages := provider.translateMessages(core.ProviderRequest{
		Messages: []core.Message{
			{
				Role: "user",
				Content: []core.ContentBlock{
					core.Text("inspect"),
					{Type: core.BlockImage, Source: &core.ImageSource{Type: "base64", MediaType: "image/png", Data: "abc"}},
				},
			},
			{
				Role: "user",
				Content: []core.ContentBlock{
					core.ToolResultBlock("call_1", []core.ContentBlock{
						core.Text("ok"),
						{Type: core.BlockImage, Source: &core.ImageSource{Type: "url", URL: "https://example.test/image.png"}},
					}, false),
				},
			},
		},
	})
	expected := []map[string]interface{}{
		{
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "text", "text": "inspect"},
				{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/png;base64,abc"}},
			},
		},
		{"role": "tool", "tool_call_id": "call_1", "content": "ok\n[image]"},
	}
	if !reflect.DeepEqual(messages, expected) {
		t.Fatalf("unexpected chat image translation\nexpected: %s\nactual:   %s", pretty(expected), pretty(messages))
	}
}

func TestOpenAIResponsesTranslationIncludesFunctionCallsAndOutputs(t *testing.T) {
	input := responsesInput([]core.Message{
		{Role: "user", Content: []core.ContentBlock{core.Text("hello")}},
		{
			Role: "assistant",
			Content: []core.ContentBlock{
				core.ToolUse("call_1", "Read", map[string]interface{}{"file_path": "go.mod"}),
				core.Text("after"),
			},
		},
		{Role: "user", Content: []core.ContentBlock{core.ToolResultBlock("call_1", "module ok", false)}},
	})
	expected := []map[string]interface{}{
		{"type": "message", "role": "user", "content": []map[string]interface{}{{"type": "input_text", "text": "hello"}}},
		{"type": "function_call", "call_id": "call_1", "name": "Read", "arguments": `{"file_path":"go.mod"}`},
		{"type": "message", "role": "assistant", "content": []map[string]interface{}{{"type": "output_text", "text": "after"}}},
		{"type": "function_call_output", "call_id": "call_1", "output": "module ok"},
	}
	if !reflect.DeepEqual(input, expected) {
		t.Fatalf("unexpected responses input translation\nexpected: %s\nactual:   %s", pretty(expected), pretty(input))
	}

	tools := responsesTools([]core.ToolSchema{{
		Name:        "Read",
		Description: "read a file",
		InputSchema: map[string]interface{}{"type": "object"},
	}})
	expectedTools := []map[string]interface{}{{
		"type":        "function",
		"name":        "Read",
		"description": "read a file",
		"parameters":  map[string]interface{}{"type": "object"},
	}}
	if !reflect.DeepEqual(tools, expectedTools) {
		t.Fatalf("unexpected responses tools translation\nexpected: %s\nactual:   %s", pretty(expectedTools), pretty(tools))
	}
}

func TestOpenAIResponsesInputReplaysPreviousResponseMetadata(t *testing.T) {
	input, previous := responsesInputAndPrevious([]core.Message{
		{Role: "user", Content: []core.ContentBlock{core.Text("old")}},
		{
			Role:    "assistant",
			Content: []core.ContentBlock{core.Text("from metadata")},
			ProviderMetadata: core.MessageProviderMetadata{
				OpenAIResponses: &core.OpenAIResponsesMetadata{
					ResponseID: "resp_1",
					OutputItems: []map[string]interface{}{
						{"id": "item_1", "type": "message", "role": "assistant"},
					},
				},
			},
		},
		{Role: "user", Content: []core.ContentBlock{core.Text("new")}},
	})
	expected := []map[string]interface{}{
		{"id": "item_1", "type": "message", "role": "assistant"},
		{"type": "message", "role": "assistant", "content": []map[string]interface{}{{"type": "output_text", "text": "from metadata"}}},
		{"type": "message", "role": "user", "content": []map[string]interface{}{{"type": "input_text", "text": "new"}}},
	}
	if previous != "resp_1" {
		t.Fatalf("expected previous response id, got %q", previous)
	}
	if !reflect.DeepEqual(input, expected) {
		t.Fatalf("unexpected responses replay input\nexpected: %s\nactual:   %s", pretty(expected), pretty(input))
	}
}

func TestOpenAIStopTranslation(t *testing.T) {
	tests := map[string]core.StopReason{
		"stop":           core.StopEndTurn,
		"tool_calls":     core.StopToolUse,
		"length":         core.StopMaxTokens,
		"content_filter": core.StopRefusal,
		"unknown":        core.StopError,
	}
	for input, expected := range tests {
		if got := mapOpenAIStop(input); got != expected {
			t.Fatalf("expected %q to map to %s, got %s", input, expected, got)
		}
	}
}

func pretty(value interface{}) string {
	raw, _ := json.MarshalIndent(value, "", "  ")
	return string(raw)
}
