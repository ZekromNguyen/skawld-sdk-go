package providers

import (
	"context"
	"net/http"
	"os"

	"github.com/skawld/skawld-sdk-go/core"
)

type AnthropicOptions struct {
	APIKey           string
	BaseURL          string
	DefaultHeaders   map[string]string
	HTTPClient       *http.Client
	MaxSSEEventBytes int
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

func (p *AnthropicProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult)
	go func() {
		defer close(out)
		if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "message_start", Model: req.Model}) {
			return
		}
		maxTokens := 32768
		if req.MaxOutputTokens != nil {
			maxTokens = *req.MaxOutputTokens
		}
		payload := map[string]interface{}{
			"model":      req.Model,
			"system":     anthropicSystem(req.System, req.CachePrompt, req.CacheTTL),
			"tools":      anthropicTools(req.Tools, req.CachePrompt, req.CacheTTL),
			"messages":   anthropicMessages(req.Messages, req.CachePrompt, req.CacheTTL),
			"max_tokens": maxTokens,
			"stream":     true,
		}
		if req.Temperature != nil {
			payload["temperature"] = *req.Temperature
		}
		if thinking := anthropicThinking(req.Thinking, req.Effort); thinking != nil {
			payload["thinking"] = thinking
		}
		headers := map[string]string{
			"x-api-key":         p.opts.APIKey,
			"anthropic-version": "2023-06-01",
		}
		for k, v := range p.opts.DefaultHeaders {
			headers[k] = v
		}
		wire := postSSE(ctx, p.opts.HTTPClient, p.opts.BaseURL+"/messages", headers, payload, p.opts.MaxSSEEventBytes)
		toolByIndex := map[int]string{}
		stop := core.StopEndTurn
		usage := core.Usage{}
		for result := range wire {
			if result.Err != nil {
				sendProviderError(ctx, out, result.Err)
				return
			}
			ev := result.Event
			switch ev["type"] {
			case "message_start":
				if msg, ok := ev["message"].(map[string]interface{}); ok {
					if u, ok := msg["usage"].(map[string]interface{}); ok {
						usage = anthropicUsage(u, usage)
					}
				}
			case "content_block_start":
				idx := intNum(ev["index"])
				block, _ := ev["content_block"].(map[string]interface{})
				if block["type"] == "tool_use" {
					id, _ := block["id"].(string)
					name, _ := block["name"].(string)
					toolByIndex[idx] = id
					if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "tool_use_start", ID: id, Name: name}) {
						return
					}
				}
			case "content_block_delta":
				idx := intNum(ev["index"])
				d, _ := ev["delta"].(map[string]interface{})
				switch d["type"] {
				case "text_delta":
					if text, ok := d["text"].(string); ok {
						if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "text_delta", Text: text}) {
							return
						}
					}
				case "thinking_delta":
					if text, ok := d["thinking"].(string); ok {
						if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "thinking_delta", Text: text}) {
							return
						}
					}
				case "signature_delta":
					if sig, ok := d["signature"].(string); ok {
						if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "thinking_delta", Signature: sig}) {
							return
						}
					}
				case "input_json_delta":
					if pj, ok := d["partial_json"].(string); ok {
						if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: toolByIndex[idx], JSONDelta: pj}) {
							return
						}
					}
				}
			case "content_block_stop":
				idx := intNum(ev["index"])
				if id := toolByIndex[idx]; id != "" {
					if !sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "tool_use_end", ID: id}) {
						return
					}
				}
			case "message_delta":
				if d, ok := ev["delta"].(map[string]interface{}); ok {
					if sr, ok := d["stop_reason"].(string); ok {
						stop = mapAnthropicStop(sr)
					}
				}
				if u, ok := ev["usage"].(map[string]interface{}); ok {
					usage = anthropicUsage(u, usage)
				}
			}
		}
		sendProviderEvent(ctx, out, core.ProviderStreamEvent{Type: "message_end", StopReason: stop, Usage: usage})
	}()
	return out
}

func anthropicTools(tools []core.ToolSchema, cachePrompt bool, cacheTTL string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for i, t := range tools {
		wire := map[string]interface{}{"name": t.Name, "description": t.Description, "input_schema": t.InputSchema}
		if cachePrompt && i == len(tools)-1 {
			wire["cache_control"] = anthropicCacheControl(cacheTTL)
		}
		out = append(out, wire)
	}
	return out
}

func anthropicSystem(system []core.SystemBlock, cachePrompt bool, cacheTTL string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(system))
	for _, block := range system {
		wire := map[string]interface{}{"type": "text", "text": block.Text}
		if cachePrompt && block.Cacheable {
			wire["cache_control"] = anthropicCacheControl(cacheTTL)
		}
		out = append(out, wire)
	}
	return out
}

func anthropicMessages(messages []core.Message, cachePrompt bool, cacheTTL string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for msgIndex, msg := range messages {
		content := make([]map[string]interface{}, 0, len(msg.Content))
		for blockIndex, block := range msg.Content {
			cacheBlock := cachePrompt && msgIndex == len(messages)-2 && blockIndex == len(msg.Content)-1
			switch block.Type {
			case core.BlockText:
				wire := map[string]interface{}{"type": "text", "text": block.Text}
				if cacheBlock {
					wire["cache_control"] = anthropicCacheControl(cacheTTL)
				}
				content = append(content, wire)
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
				if cacheBlock {
					wire["cache_control"] = anthropicCacheControl(cacheTTL)
				}
				content = append(content, wire)
			case core.BlockImage:
				if block.Source != nil {
					wire := map[string]interface{}{"type": "image", "source": anthropicImageSource(*block.Source)}
					if cacheBlock {
						wire["cache_control"] = anthropicCacheControl(cacheTTL)
					}
					content = append(content, wire)
				}
			}
		}
		out = append(out, map[string]interface{}{"role": msg.Role, "content": content})
	}
	return out
}

func anthropicCacheControl(cacheTTL string) map[string]interface{} {
	cacheControl := map[string]interface{}{"type": "ephemeral"}
	if cacheTTL != "" {
		cacheControl["ttl"] = cacheTTL
	}
	return cacheControl
}

func anthropicThinking(thinking map[string]interface{}, effort string) map[string]interface{} {
	if len(thinking) > 0 {
		out := make(map[string]interface{}, len(thinking))
		for k, v := range thinking {
			out[k] = v
		}
		return out
	}
	budget := anthropicEffortBudget(effort)
	if budget <= 0 {
		return nil
	}
	return map[string]interface{}{"type": "enabled", "budget_tokens": budget}
}

func anthropicEffortBudget(effort string) int {
	switch effort {
	case "low":
		return 1024
	case "medium":
		return 4096
	case "high":
		return 8192
	default:
		return 0
	}
}

func anthropicUsage(raw map[string]interface{}, prev core.Usage) core.Usage {
	usage := prev
	if n := intNum(raw["input_tokens"]); n > 0 {
		usage.InputTokens = n
	}
	if n := intNum(raw["output_tokens"]); n > 0 {
		usage.OutputTokens = n
	}
	if n := intNum(raw["cache_read_input_tokens"]); n > 0 {
		usage.CacheReadTokens = n
	}
	if n := intNum(raw["cache_creation_input_tokens"]); n > 0 {
		usage.CacheCreationTokens = n
	}
	if cacheCreation, ok := raw["cache_creation"].(map[string]interface{}); ok {
		usage.CacheCreationTokens += intNum(cacheCreation["ephemeral_5m_input_tokens"])
		usage.CacheCreationTokens += intNum(cacheCreation["ephemeral_1h_input_tokens"])
	}
	return usage
}

func mapAnthropicStop(stop string) core.StopReason {
	switch stop {
	case "end_turn":
		return core.StopEndTurn
	case "tool_use":
		return core.StopToolUse
	case "max_tokens":
		return core.StopMaxTokens
	case "stop_sequence":
		return core.StopSequence
	case "refusal":
		return core.StopRefusal
	default:
		return core.StopError
	}
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
