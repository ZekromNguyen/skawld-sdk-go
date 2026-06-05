package providers

import (
	"context"
	"os"

	"github.com/skawld/skawld-sdk-go/core"
)

type AnthropicOptions struct {
	APIKey         string
	BaseURL        string
	DefaultHeaders map[string]string
}

type AnthropicProvider struct {
	opts AnthropicOptions
}

func NewAnthropicProvider(opts AnthropicOptions) *AnthropicProvider {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.anthropic.com/v1"
	}
	if opts.APIKey == "" {
		opts.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	return &AnthropicProvider{opts: opts}
}

func (p *AnthropicProvider) ID() string { return "anthropic" }
func (p *AnthropicProvider) ContextWindow(model core.ModelID) int {
	return 200000
}

func (p *AnthropicProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		maxTokens := 32768
		if req.MaxOutputTokens != nil {
			maxTokens = *req.MaxOutputTokens
		}
		payload := map[string]interface{}{
			"model":      req.Model,
			"system":     anthropicSystem(req.System, req.CachePrompt, req.CacheTTL),
			"tools":      anthropicTools(req.Tools),
			"messages":   anthropicMessages(req.Messages),
			"max_tokens": maxTokens,
			"stream":     true,
		}
		if req.Temperature != nil {
			payload["temperature"] = *req.Temperature
		}
		headers := map[string]string{
			"x-api-key":         p.opts.APIKey,
			"anthropic-version": "2023-06-01",
		}
		for k, v := range p.opts.DefaultHeaders {
			headers[k] = v
		}
		wire, wireErrs := postSSE(ctx, httpClient(), p.opts.BaseURL+"/messages", headers, payload)
		toolByIndex := map[int]string{}
		stop := core.StopEndTurn
		usage := core.Usage{}
		for ev := range wire {
			switch ev["type"] {
			case "message_start":
				if msg, ok := ev["message"].(map[string]interface{}); ok {
					if u, ok := msg["usage"].(map[string]interface{}); ok {
						usage.InputTokens = intNum(u["input_tokens"])
						usage.OutputTokens = intNum(u["output_tokens"])
					}
				}
			case "content_block_start":
				idx := intNum(ev["index"])
				block, _ := ev["content_block"].(map[string]interface{})
				if block["type"] == "tool_use" {
					id, _ := block["id"].(string)
					name, _ := block["name"].(string)
					toolByIndex[idx] = id
					out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: id, Name: name}
				}
			case "content_block_delta":
				idx := intNum(ev["index"])
				d, _ := ev["delta"].(map[string]interface{})
				switch d["type"] {
				case "text_delta":
					if text, ok := d["text"].(string); ok {
						out <- core.ProviderStreamEvent{Type: "text_delta", Text: text}
					}
				case "thinking_delta":
					if text, ok := d["thinking"].(string); ok {
						out <- core.ProviderStreamEvent{Type: "thinking_delta", Text: text}
					}
				case "input_json_delta":
					if pj, ok := d["partial_json"].(string); ok {
						out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: toolByIndex[idx], JSONDelta: pj}
					}
				}
			case "content_block_stop":
				idx := intNum(ev["index"])
				if id := toolByIndex[idx]; id != "" {
					out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: id}
				}
			case "message_delta":
				if d, ok := ev["delta"].(map[string]interface{}); ok {
					if sr, ok := d["stop_reason"].(string); ok {
						stop = core.StopReason(sr)
					}
				}
				if u, ok := ev["usage"].(map[string]interface{}); ok {
					usage.InputTokens = intNum(u["input_tokens"])
					usage.OutputTokens = intNum(u["output_tokens"])
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

func anthropicTools(tools []core.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{"name": t.Name, "description": t.Description, "input_schema": t.InputSchema})
	}
	return out
}

func anthropicSystem(system []core.SystemBlock, cachePrompt bool, cacheTTL string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(system))
	for _, block := range system {
		wire := map[string]interface{}{"type": "text", "text": block.Text}
		if cachePrompt && block.Cacheable {
			cacheControl := map[string]interface{}{"type": "ephemeral"}
			if cacheTTL != "" {
				cacheControl["ttl"] = cacheTTL
			}
			wire["cache_control"] = cacheControl
		}
		out = append(out, wire)
	}
	return out
}

func anthropicMessages(messages []core.Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		content := make([]map[string]interface{}, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch block.Type {
			case core.BlockText:
				content = append(content, map[string]interface{}{"type": "text", "text": block.Text})
			case core.BlockThinking:
				wire := map[string]interface{}{"type": "thinking", "thinking": block.Thinking}
				if block.Signature != "" {
					wire["signature"] = block.Signature
				}
				content = append(content, wire)
			case core.BlockToolUse:
				content = append(content, map[string]interface{}{"type": "tool_use", "id": block.ID, "name": block.Name, "input": block.Input})
			case core.BlockToolResult:
				wire := map[string]interface{}{"type": "tool_result", "tool_use_id": block.ToolUseID, "content": block.StringContent()}
				if block.IsError {
					wire["is_error"] = true
				}
				content = append(content, wire)
			case core.BlockImage:
				if block.Source != nil {
					content = append(content, map[string]interface{}{"type": "image", "source": anthropicImageSource(*block.Source)})
				}
			}
		}
		out = append(out, map[string]interface{}{"role": msg.Role, "content": content})
	}
	return out
}

func anthropicImageSource(source core.ImageSource) map[string]interface{} {
	wire := map[string]interface{}{"type": source.Type}
	if source.MediaType != "" {
		wire["media_type"] = source.MediaType
	}
	if source.Data != "" {
		wire["data"] = source.Data
	}
	if source.URL != "" {
		wire["url"] = source.URL
	}
	return wire
}
