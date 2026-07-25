package providers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type OpenAIResponsesProvider struct {
	opts OpenAIOptions
}

func NewOpenAIResponsesProvider(opts OpenAIOptions) *OpenAIResponsesProvider {
	base := NewOpenAIChatCompletionsProvider(opts)
	return &OpenAIResponsesProvider{opts: base.opts}
}

func (p *OpenAIResponsesProvider) ID() string { return "openai-responses" }
func (p *OpenAIResponsesProvider) ContextWindow(model core.ModelID) int {
	return NewOpenAIChatCompletionsProvider(p.opts).ContextWindow(model)
}

func (p *OpenAIResponsesProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult)
	go func() {
		defer close(out)
		if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "message_start", Model: req.Model}) {
			return
		}
		input, previousResponseID := responsesInputAndPrevious(req.Messages)
		payload := map[string]interface{}{"model": req.Model, "input": input, "stream": true}
		if previousResponseID != "" {
			payload["previous_response_id"] = previousResponseID
		}
		var instructions strings.Builder
		for i, b := range req.System {
			if i > 0 {
				instructions.WriteString("\n\n")
			}
			instructions.WriteString(b.Text)
		}
		if instructions.Len() > 0 {
			payload["instructions"] = instructions.String()
		}
		if len(req.Tools) > 0 {
			payload["tools"] = responsesTools(req.Tools)
		}
		if req.MaxOutputTokens != nil {
			payload["max_output_tokens"] = *req.MaxOutputTokens
		}
		if req.Temperature != nil {
			payload["temperature"] = *req.Temperature
		}
		if reasoning := responsesReasoning(req.Thinking, req.Effort); reasoning != nil {
			payload["reasoning"] = reasoning
		}
		headers := map[string]string{"authorization": "Bearer " + p.opts.APIKey}
		for k, v := range p.opts.DefaultHeaders {
			headers[k] = v
		}
		wire := postSSE(ctx, p.opts.HTTPClient, p.opts.BaseURL+"/responses", headers, payload, p.opts.MaxSSEEventBytes)
		itemToCall := map[string]string{}
		hasFunctionCall := false
		stop := core.StopEndTurn
		usage := core.Usage{}
		responseID := ""
		var outputItems []map[string]interface{}
		for result := range wire {
			if result.Err != nil {
				sendProviderError(ctx, out, result.Err)
				return
			}
			ev := result.Event
			switch ev["type"] {
			case "response.created":
				if resp, ok := ev["response"].(map[string]interface{}); ok {
					responseID = firstString(responseID, stringValue(resp["id"]))
				}
			case "response.output_item.added":
				item, _ := ev["item"].(map[string]interface{})
				outputItems = upsertResponseOutputItem(outputItems, item)
				if item["type"] == "function_call" {
					id, _ := item["id"].(string)
					callID, _ := item["call_id"].(string)
					name, _ := item["name"].(string)
					if id != "" && callID != "" && name != "" {
						itemToCall[id] = callID
						hasFunctionCall = true
						if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "tool_use_start", ID: callID, Name: name}) {
							return
						}
					}
				}
			case "response.output_text.delta":
				if d, ok := ev["delta"].(string); ok {
					if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "text_delta", Text: d}) {
						return
					}
				}
			case "response.reasoning_summary_text.delta":
				if d, ok := ev["delta"].(string); ok {
					if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "thinking_delta", Text: d}) {
						return
					}
				}
			case "response.function_call_arguments.delta":
				itemID, _ := ev["item_id"].(string)
				if d, ok := ev["delta"].(string); ok {
					if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: itemToCall[itemID], JSONDelta: d}) {
						return
					}
				}
			case "response.output_item.done":
				item, _ := ev["item"].(map[string]interface{})
				outputItems = upsertResponseOutputItem(outputItems, item)
				if item["type"] == "function_call" {
					id, _ := item["id"].(string)
					if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "tool_use_end", ID: itemToCall[id]}) {
						return
					}
				}
			case "response.completed", "response.incomplete", "response.failed":
				resp, _ := ev["response"].(map[string]interface{})
				responseID = firstString(responseID, stringValue(resp["id"]))
				if rawOutput, ok := resp["output"].([]interface{}); ok {
					outputItems = responseOutputItems(rawOutput)
				}
				if u, ok := resp["usage"].(map[string]interface{}); ok {
					usage = core.Usage{InputTokens: intNum(u["input_tokens"]), OutputTokens: intNum(u["output_tokens"])}
				}
				stop = mapResponsesStop(resp, hasFunctionCall)
			}
		}
		meta := core.MessageProviderMetadata{}
		if responseID != "" || len(outputItems) > 0 {
			meta.OpenAIResponses = &core.OpenAIResponsesMetadata{ResponseID: responseID, OutputItems: outputItems}
		}
		sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "message_end", StopReason: stop, Usage: usage, ProviderMetadata: meta})
	}()
	return out
}

func responsesTools(tools []core.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.InputSchema})
	}
	return out
}

func responsesInput(messages []core.Message) []map[string]interface{} {
	input, _ := responsesInputAndPrevious(messages)
	return input
}

func responsesInputAndPrevious(messages []core.Message) ([]map[string]interface{}, string) {
	var out []map[string]interface{}
	previousResponseID := ""
	for _, msg := range messages {
		if meta := msg.ProviderMetadata.OpenAIResponses; meta != nil {
			if meta.ResponseID != "" {
				previousResponseID = meta.ResponseID
				out = nil
			}
			if len(meta.OutputItems) > 0 {
				out = append(out, cloneResponseItems(meta.OutputItems)...)
			}
		}
		if msg.Role == "assistant" {
			var text strings.Builder
			for _, b := range msg.Content {
				if b.Type == core.BlockText {
					text.WriteString(b.Text)
				}
				if b.Type == core.BlockToolUse {
					raw, _ := json.Marshal(b.Input)
					out = append(out, map[string]interface{}{"type": "function_call", "call_id": b.ID, "name": b.Name, "arguments": string(raw)})
				}
			}
			if text.Len() > 0 {
				out = append(out, map[string]interface{}{"type": "message", "role": "assistant", "content": []map[string]interface{}{{"type": "output_text", "text": text.String()}}})
			}
			continue
		}
		var parts []map[string]interface{}
		for _, b := range msg.Content {
			if b.Type == core.BlockToolResult {
				out = append(out, map[string]interface{}{"type": "function_call_output", "call_id": b.ToolUseID, "output": b.StringContent()})
			}
			if b.Type == core.BlockText {
				parts = append(parts, map[string]interface{}{"type": "input_text", "text": b.Text})
			}
		}
		if len(parts) > 0 {
			out = append(out, map[string]interface{}{"type": "message", "role": "user", "content": parts})
		}
	}
	return out, previousResponseID
}

func responsesReasoning(thinking map[string]interface{}, effort string) map[string]interface{} {
	if len(thinking) > 0 {
		out := make(map[string]interface{}, len(thinking))
		for k, v := range thinking {
			out[k] = v
		}
		return out
	}
	if effort == "" {
		return nil
	}
	return map[string]interface{}{"effort": effort, "summary": "auto"}
}

func mapResponsesStop(resp map[string]interface{}, hasFunctionCall bool) core.StopReason {
	status, _ := resp["status"].(string)
	if status == "completed" && hasFunctionCall {
		return core.StopToolUse
	}
	if status == "completed" {
		return core.StopEndTurn
	}
	if status == "incomplete" {
		if details, ok := resp["incomplete_details"].(map[string]interface{}); ok {
			switch details["reason"] {
			case "max_output_tokens":
				return core.StopMaxTokens
			case "content_filter":
				return core.StopRefusal
			}
		}
		return core.StopMaxTokens
	}
	if status == "failed" {
		return core.StopError
	}
	return core.StopError
}

func responseOutputItems(raw []interface{}) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(raw))
	for _, value := range raw {
		if item, ok := value.(map[string]interface{}); ok {
			items = upsertResponseOutputItem(items, item)
		}
	}
	return items
}

func upsertResponseOutputItem(items []map[string]interface{}, item map[string]interface{}) []map[string]interface{} {
	if item == nil {
		return items
	}
	id := stringValue(item["id"])
	cloned := cloneMap(item)
	if id == "" {
		return append(items, cloned)
	}
	for i, existing := range items {
		if stringValue(existing["id"]) == id {
			items[i] = cloned
			return items
		}
	}
	return append(items, cloned)
}

func cloneResponseItems(items []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, cloneMap(item))
	}
	return out
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = cloneJSONValue(v)
	}
	return out
}

func cloneJSONValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneMap(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = cloneJSONValue(v[i])
		}
		return out
	default:
		return v
	}
}
