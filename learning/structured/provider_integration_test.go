package structured

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/providers"
)

func TestExtractorThroughProductionProviderTransports(t *testing.T) {
	document := validDocumentJSON(t, "event-1")
	cases := []struct {
		name     string
		path     string
		provider func(string, *http.Client) core.Provider
		stream   func(*testing.T, http.ResponseWriter, string)
	}{
		{
			name: "openai responses", path: "/responses",
			provider: func(url string, client *http.Client) core.Provider {
				return providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{
					APIKey: "test", BaseURL: url, HTTPClient: client,
				})
			},
			stream: writeResponsesExtractionStream,
		},
		{
			name: "openai chat completions", path: "/chat/completions",
			provider: func(url string, client *http.Client) core.Provider {
				return providers.NewOpenAIChatCompletionsProvider(providers.OpenAIOptions{
					APIKey: "test", BaseURL: url, HTTPClient: client,
				})
			},
			stream: writeChatExtractionStream,
		},
		{
			name: "anthropic messages", path: "/messages",
			provider: func(url string, client *http.Client) core.Provider {
				return providers.NewAnthropicProvider(providers.AnthropicOptions{
					APIKey: "test", BaseURL: url, HTTPClient: client,
				})
			},
			stream: writeAnthropicExtractionStream,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var payload map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != testCase.path {
					t.Errorf("request path = %q, want %q", r.URL.Path, testCase.path)
					http.Error(w, "wrong path", http.StatusNotFound)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode provider request: %v", err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				w.Header().Set("content-type", "text/event-stream")
				testCase.stream(t, w, document)
			}))
			defer server.Close()

			extractor, err := New(Options{
				Provider: testCase.provider(server.URL, server.Client()),
				Model:    "transport-test",
				Tools: []ToolDefinition{{
					Name: "erp.lookup",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"invoice_id": map[string]interface{}{"type": "string"},
						},
					},
				}},
				MaxProviderCalls: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := extractor.ExtractDetailed(
				testContext(), extractionRequest(observation.TrustApplicationEvent),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Candidate.Steps) != 1 ||
				result.Candidate.Steps[0].Tool == nil ||
				result.Candidate.Steps[0].Tool.Name != "erp.lookup" {
				t.Fatalf("unexpected candidate: %+v", result.Candidate)
			}
			if result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 7 {
				t.Fatalf("usage = %+v", result.Usage)
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), submitToolName) {
				t.Fatalf("provider payload omitted structured output tool: %s", raw)
			}
		})
	}
}

func writeResponsesExtractionStream(t *testing.T, w http.ResponseWriter, document string) {
	t.Helper()
	left, right := splitDocument(document)
	writeExtractorSSE(t, w, map[string]interface{}{
		"type": "response.output_item.added",
		"item": map[string]interface{}{
			"type": "function_call", "id": "item-1", "call_id": "call-1",
			"name": submitToolName,
		},
	})
	for _, delta := range []string{left, right} {
		writeExtractorSSE(t, w, map[string]interface{}{
			"type": "response.function_call_arguments.delta", "item_id": "item-1",
			"delta": delta,
		})
	}
	writeExtractorSSE(t, w, map[string]interface{}{
		"type": "response.output_item.done",
		"item": map[string]interface{}{
			"type": "function_call", "id": "item-1", "call_id": "call-1",
			"name": submitToolName, "arguments": document,
		},
	})
	writeExtractorSSE(t, w, map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id": "response-1", "status": "completed",
			"usage": map[string]interface{}{"input_tokens": 11, "output_tokens": 7},
		},
	})
}

func writeChatExtractionStream(t *testing.T, w http.ResponseWriter, document string) {
	t.Helper()
	left, right := splitDocument(document)
	writeExtractorSSE(t, w, map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"delta": map[string]interface{}{
				"tool_calls": []interface{}{map[string]interface{}{
					"index": 0, "id": "call-1",
					"function": map[string]interface{}{
						"name": submitToolName, "arguments": left,
					},
				}},
			},
		}},
	})
	writeExtractorSSE(t, w, map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"delta": map[string]interface{}{
				"tool_calls": []interface{}{map[string]interface{}{
					"index": 0, "function": map[string]interface{}{"arguments": right},
				}},
			},
		}},
	})
	writeExtractorSSE(t, w, map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{"finish_reason": "tool_calls"}},
	})
	writeExtractorSSE(t, w, map[string]interface{}{
		"choices": []interface{}{},
		"usage":   map[string]interface{}{"prompt_tokens": 11, "completion_tokens": 7},
	})
}

func writeAnthropicExtractionStream(t *testing.T, w http.ResponseWriter, document string) {
	t.Helper()
	left, right := splitDocument(document)
	writeExtractorSSE(t, w, map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"usage": map[string]interface{}{"input_tokens": 11},
		},
	})
	writeExtractorSSE(t, w, map[string]interface{}{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]interface{}{
			"type": "tool_use", "id": "call-1", "name": submitToolName,
		},
	})
	for _, delta := range []string{left, right} {
		writeExtractorSSE(t, w, map[string]interface{}{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]interface{}{
				"type": "input_json_delta", "partial_json": delta,
			},
		})
	}
	writeExtractorSSE(t, w, map[string]interface{}{"type": "content_block_stop", "index": 0})
	writeExtractorSSE(t, w, map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": "tool_use"},
		"usage": map[string]interface{}{"output_tokens": 7},
	})
}

func splitDocument(document string) (string, string) {
	midpoint := len(document) / 2
	return document[:midpoint], document[midpoint:]
}

func writeExtractorSSE(t *testing.T, w http.ResponseWriter, event map[string]interface{}) {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		t.Fatal(err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
