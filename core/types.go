package core

import "encoding/json"

type ModelID string
type PermissionMode string
type StopReason string
type ToolScope string

const (
	PermissionModeDefault     PermissionMode = "default"
	PermissionModeAcceptEdits PermissionMode = "acceptEdits"
	PermissionModeYolo        PermissionMode = "yolo"

	StopEndTurn     StopReason = "end_turn"
	StopToolUse     StopReason = "tool_use"
	StopMaxTokens   StopReason = "max_tokens"
	StopSequence    StopReason = "stop_sequence"
	StopRefusal     StopReason = "refusal"
	StopError       StopReason = "error"
	ToolScopeRead   ToolScope  = "read"
	ToolScopeWrite  ToolScope  = "write"
	ToolScopeExec   ToolScope  = "exec"
	BlockText       string     = "text"
	BlockToolUse    string     = "tool_use"
	BlockToolResult string     = "tool_result"
	BlockThinking   string     = "thinking"
	BlockImage      string     = "image"
)

type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
}

func AddUsage(a, b Usage) Usage {
	return Usage{
		InputTokens:         a.InputTokens + b.InputTokens,
		OutputTokens:        a.OutputTokens + b.OutputTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
	}
}

type Message struct {
	Role             string                  `json:"role"`
	Content          []ContentBlock          `json:"content"`
	ProviderMetadata MessageProviderMetadata `json:"provider_metadata,omitempty"`
}

type MessageProviderMetadata struct {
	OpenAIResponses *OpenAIResponsesMetadata `json:"openai_responses,omitempty"`
}

func (m MessageProviderMetadata) Empty() bool {
	return m.OpenAIResponses == nil
}

type OpenAIResponsesMetadata struct {
	ResponseID  string                   `json:"response_id,omitempty"`
	OutputItems []map[string]interface{} `json:"output_items,omitempty"`
}

type ContentBlock struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   interface{}            `json:"content,omitempty"`
	IsError   bool                   `json:"is_error,omitempty"`
	Thinking  string                 `json:"thinking,omitempty"`
	Signature string                 `json:"signature,omitempty"`
	Source    *ImageSource           `json:"source,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

func Text(text string) ContentBlock {
	return ContentBlock{Type: BlockText, Text: text}
}

func ToolUse(id, name string, input map[string]interface{}) ContentBlock {
	return ContentBlock{Type: BlockToolUse, ID: id, Name: name, Input: input}
}

func ToolResultBlock(toolUseID string, content interface{}, isError bool) ContentBlock {
	return ContentBlock{Type: BlockToolResult, ToolUseID: toolUseID, Content: content, IsError: isError}
}

func (b ContentBlock) StringContent() string {
	if s, ok := b.Content.(string); ok {
		return s
	}
	raw, _ := json.Marshal(b.Content)
	return string(raw)
}
