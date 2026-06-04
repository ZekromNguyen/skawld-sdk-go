package core

type EventType string

const (
	EventSystem            EventType = "system"
	EventAssistant         EventType = "assistant"
	EventUser              EventType = "user"
	EventPartialAssistant  EventType = "partial_assistant"
	EventToolCallStart     EventType = "tool_call_start"
	EventToolCallEnd       EventType = "tool_call_end"
	EventPermissionRequest EventType = "permission_request"
	EventUsage             EventType = "usage"
	EventCompaction        EventType = "compaction"
	EventResult            EventType = "result"
	EventError             EventType = "error"
	EventSkillsLoaded      EventType = "skills_loaded"
	EventSkillInvoked      EventType = "skill_invoked"
	EventSkillCompleted    EventType = "skill_completed"
	EventSubagent          EventType = "subagent_event"
)

type Event struct {
	Type           EventType              `json:"type"`
	Subtype        string                 `json:"subtype,omitempty"`
	SessionID      string                 `json:"session_id,omitempty"`
	RunID          string                 `json:"run_id,omitempty"`
	Model          ModelID                `json:"model,omitempty"`
	Tools          []string               `json:"tools,omitempty"`
	PermissionMode PermissionMode         `json:"permission_mode,omitempty"`
	CWD            string                 `json:"cwd,omitempty"`
	Message        Message                `json:"message,omitempty"`
	StopReason     StopReason             `json:"stop_reason,omitempty"`
	Delta          map[string]interface{} `json:"delta,omitempty"`
	ToolUseID      string                 `json:"tool_use_id,omitempty"`
	ToolName       string                 `json:"tool_name,omitempty"`
	Input          map[string]interface{} `json:"input,omitempty"`
	IsError        bool                   `json:"is_error,omitempty"`
	DurationMS     int64                  `json:"duration_ms,omitempty"`
	Requests       []PermissionRequest    `json:"requests,omitempty"`
	Usage          Usage                  `json:"usage,omitempty"`
	Cumulative     Usage                  `json:"cumulative,omitempty"`
	TotalUsage     Usage                  `json:"total_usage,omitempty"`
	FinalText      string                 `json:"final_text,omitempty"`
	Error          *EventErrorPayload     `json:"error,omitempty"`
	MessagesBefore int                    `json:"messages_before,omitempty"`
	MessagesAfter  int                    `json:"messages_after,omitempty"`
	TokensBefore   int                    `json:"tokens_before,omitempty"`
	TokensAfter    int                    `json:"tokens_after,omitempty"`
	Strategy       string                 `json:"strategy,omitempty"`
}

type PermissionRequest struct {
	ToolUseID string                 `json:"tool_use_id"`
	ToolName  string                 `json:"tool_name"`
	Input     map[string]interface{} `json:"input"`
	Summary   string                 `json:"summary"`
}

type EventErrorPayload struct {
	Name      string `json:"name"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
