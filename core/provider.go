package core

import "context"

type SystemBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Cacheable bool   `json:"cacheable,omitempty"`
}

type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type ProviderRequest struct {
	Model           ModelID
	System          []SystemBlock
	Tools           []ToolSchema
	Messages        []Message
	MaxOutputTokens *int
	Temperature     *float64
	StopSequences   []string
	CachePrompt     bool
	CacheTTL        string
	Thinking        map[string]interface{}
	Effort          string
	MaxRetries      int
}

type ProviderStreamEvent struct {
	Type             string
	Model            ModelID
	Text             string
	Signature        string
	ID               string
	Name             string
	JSONDelta        string
	StopReason       StopReason
	Usage            Usage
	ProviderMetadata MessageProviderMetadata
}

type Provider interface {
	ID() string
	ContextWindow(model ModelID) int
	Stream(ctx context.Context, req ProviderRequest) (<-chan ProviderStreamEvent, <-chan error)
}
