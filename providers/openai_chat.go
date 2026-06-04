package providers

import (
	"context"
	"encoding/json"
	"os"

	"github.com/skawld/skawld-sdk-go/core"
)

type OpenAIOptions struct {
	APIKey                string
	BaseURL               string
	DefaultHeaders        map[string]string
	ContextWindowOverride func(core.ModelID) int
}

type OpenAIChatCompletionsProvider struct {
	opts OpenAIOptions
}

func NewOpenAIChatCompletionsProvider(opts OpenAIOptions) *OpenAIChatCompletionsProvider {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.openai.com/v1"
	}
	if opts.APIKey == "" {
		opts.APIKey = os.Getenv("OPENAI_API_KEY")
	}
	return &OpenAIChatCompletionsProvider{opts: opts}
}

func (p *OpenAIChatCompletionsProvider) ID() string { return "openai-chat" }
func (p *OpenAIChatCompletionsProvider) ContextWindow(model core.ModelID) int {
	if p.opts.ContextWindowOverride != nil {
		if n := p.opts.ContextWindowOverride(model); n > 0 {
			return n
		}
	}
	switch string(model) {
	case "gpt-5":
		return 400000
	case "gpt-4.1":
		return 1000000
	case "gpt-4o":
		return 128000
	default:
		return 128000
	}
}

func (p *OpenAIChatCompletionsProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		payload := map[string]interface{}{
			"model":          req.Model,
			"messages":       p.translateMessages(req),
			"stream":         true,
			"stream_options": map[string]interface{}{"include_usage": true},
		}
		if req.MaxOutputTokens != nil {
			payload["max_tokens"] = *req.MaxOutputTokens
		}
		if req.Temperature != nil {
			payload["temperature"] = *req.Temperature
		}
		if len(req.Tools) > 0 {
			payload["tools"] = openAITools(req.Tools)
		}
		headers := map[string]string{"authorization": "Bearer " + p.opts.APIKey}
		for k, v := range p.opts.DefaultHeaders {
			headers[k] = v
		}
		wire, wireErrs := postSSE(ctx, httpClient(), p.opts.BaseURL+"/chat/completions", headers, payload)
		slots := map[int]struct{ id, name string }{}
		stop := core.StopEndTurn
		usage := core.Usage{}
		for ev := range wire {
			if u, ok := ev["usage"].(map[string]interface{}); ok {
				usage = core.Usage{InputTokens: intNum(u["prompt_tokens"]), OutputTokens: intNum(u["completion_tokens"])}
			}
			choices, _ := ev["choices"].([]interface{})
			if len(choices) == 0 {
				continue
			}
			ch, _ := choices[0].(map[string]interface{})
			if delta, ok := ch["delta"].(map[string]interface{}); ok {
				if text, ok := delta["content"].(string); ok && text != "" {
					out <- core.ProviderStreamEvent{Type: "text_delta", Text: text}
				}
				if calls, ok := delta["tool_calls"].([]interface{}); ok {
					for _, raw := range calls {
						tc, _ := raw.(map[string]interface{})
						idx := intNum(tc["index"])
						slot := slots[idx]
						if id, ok := tc["id"].(string); ok {
							slot.id = id
						}
						fn, _ := tc["function"].(map[string]interface{})
						if name, ok := fn["name"].(string); ok {
							slot.name = name
						}
						if _, exists := slots[idx]; !exists && slot.id != "" && slot.name != "" {
							out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: slot.id, Name: slot.name}
						}
						slots[idx] = slot
						if args, ok := fn["arguments"].(string); ok && args != "" && slot.id != "" {
							out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: slot.id, JSONDelta: args}
						}
					}
				}
			}
			if fr, ok := ch["finish_reason"].(string); ok && fr != "" {
				stop = mapOpenAIStop(fr)
				for i := 0; i < len(slots); i++ {
					if slot, ok := slots[i]; ok && slot.id != "" {
						out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: slot.id}
					}
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

func (p *OpenAIChatCompletionsProvider) translateMessages(req core.ProviderRequest) []map[string]interface{} {
	var out []map[string]interface{}
	if len(req.System) > 0 {
		text := ""
		for i, b := range req.System {
			if i > 0 {
				text += "\n\n"
			}
			text += b.Text
		}
		out = append(out, map[string]interface{}{"role": "system", "content": text})
	}
	for _, msg := range req.Messages {
		if msg.Role == "assistant" {
			text := ""
			var calls []map[string]interface{}
			for _, b := range msg.Content {
				if b.Type == core.BlockText {
					text += b.Text
				}
				if b.Type == core.BlockToolUse {
					raw, _ := json.Marshal(b.Input)
					calls = append(calls, map[string]interface{}{"id": b.ID, "type": "function", "function": map[string]interface{}{"name": b.Name, "arguments": string(raw)}})
				}
			}
			m := map[string]interface{}{"role": "assistant", "content": text}
			if len(calls) > 0 {
				m["tool_calls"] = calls
			}
			out = append(out, m)
			continue
		}
		for _, b := range msg.Content {
			if b.Type == core.BlockToolResult {
				out = append(out, map[string]interface{}{"role": "tool", "tool_call_id": b.ToolUseID, "content": b.StringContent()})
			}
		}
		text := ""
		for _, b := range msg.Content {
			if b.Type == core.BlockText {
				text += b.Text
			}
		}
		if text != "" {
			out = append(out, map[string]interface{}{"role": "user", "content": text})
		}
	}
	return out
}

func openAITools(tools []core.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": t.Name, "description": t.Description, "parameters": t.InputSchema}})
	}
	return out
}

func mapOpenAIStop(s string) core.StopReason {
	switch s {
	case "stop":
		return core.StopEndTurn
	case "tool_calls":
		return core.StopToolUse
	case "length":
		return core.StopMaxTokens
	case "content_filter":
		return core.StopRefusal
	default:
		return core.StopError
	}
}

func intNum(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
