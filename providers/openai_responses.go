package providers

import (
	"context"
	"encoding/json"

	"github.com/skawld/skawld-sdk-go/core"
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

func (p *OpenAIResponsesProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		payload := map[string]interface{}{"model": req.Model, "input": responsesInput(req.Messages), "stream": true}
		instructions := ""
		for i, b := range req.System {
			if i > 0 {
				instructions += "\n\n"
			}
			instructions += b.Text
		}
		if instructions != "" {
			payload["instructions"] = instructions
		}
		if len(req.Tools) > 0 {
			payload["tools"] = responsesTools(req.Tools)
		}
		if req.MaxOutputTokens != nil {
			payload["max_output_tokens"] = *req.MaxOutputTokens
		}
		headers := map[string]string{"authorization": "Bearer " + p.opts.APIKey}
		for k, v := range p.opts.DefaultHeaders {
			headers[k] = v
		}
		wire, wireErrs := postSSE(ctx, httpClient(), p.opts.BaseURL+"/responses", headers, payload)
		itemToCall := map[string]string{}
		hasFunctionCall := false
		stop := core.StopEndTurn
		usage := core.Usage{}
		for ev := range wire {
			switch ev["type"] {
			case "response.output_item.added":
				item, _ := ev["item"].(map[string]interface{})
				if item["type"] == "function_call" {
					id, _ := item["id"].(string)
					callID, _ := item["call_id"].(string)
					name, _ := item["name"].(string)
					if id != "" && callID != "" && name != "" {
						itemToCall[id] = callID
						hasFunctionCall = true
						out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: callID, Name: name}
					}
				}
			case "response.output_text.delta":
				if d, ok := ev["delta"].(string); ok {
					out <- core.ProviderStreamEvent{Type: "text_delta", Text: d}
				}
			case "response.function_call_arguments.delta":
				itemID, _ := ev["item_id"].(string)
				if d, ok := ev["delta"].(string); ok {
					out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: itemToCall[itemID], JSONDelta: d}
				}
			case "response.output_item.done":
				item, _ := ev["item"].(map[string]interface{})
				if item["type"] == "function_call" {
					id, _ := item["id"].(string)
					out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: itemToCall[id]}
				}
			case "response.completed", "response.incomplete", "response.failed":
				resp, _ := ev["response"].(map[string]interface{})
				if u, ok := resp["usage"].(map[string]interface{}); ok {
					usage = core.Usage{InputTokens: intNum(u["input_tokens"]), OutputTokens: intNum(u["output_tokens"])}
				}
				status, _ := resp["status"].(string)
				if status == "completed" && hasFunctionCall {
					stop = core.StopToolUse
				} else if status == "completed" {
					stop = core.StopEndTurn
				} else {
					stop = core.StopError
				}
			}
		}
		if err := <-wireErrs; err != nil {
			errs <- err
			return
		}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: stop, Usage: usage}
	}()
	return out, errs
}

func responsesTools(tools []core.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.InputSchema})
	}
	return out
}

func responsesInput(messages []core.Message) []map[string]interface{} {
	var out []map[string]interface{}
	for _, msg := range messages {
		if msg.Role == "assistant" {
			text := ""
			for _, b := range msg.Content {
				if b.Type == core.BlockText {
					text += b.Text
				}
				if b.Type == core.BlockToolUse {
					raw, _ := json.Marshal(b.Input)
					out = append(out, map[string]interface{}{"type": "function_call", "call_id": b.ID, "name": b.Name, "arguments": string(raw)})
				}
			}
			if text != "" {
				out = append(out, map[string]interface{}{"type": "message", "role": "assistant", "content": []map[string]interface{}{{"type": "output_text", "text": text}}})
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
	return out
}
