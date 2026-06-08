package core

import "context"

type ObservationType string

const (
	ObservationProviderAttempt    ObservationType = "provider_attempt"
	ObservationToolExecution      ObservationType = "tool_execution"
	ObservationPermissionCallback ObservationType = "permission_callback"
	ObservationCompaction         ObservationType = "compaction"
	ObservationMCPCall            ObservationType = "mcp_call"
	ObservationStoreOperation     ObservationType = "store_operation"
)

// Observer receives operational events for logging, metrics, and tracing.
// Observation values intentionally exclude raw prompts, tool inputs, provider
// payloads, headers, and secret values.
type Observer interface {
	Observe(ctx context.Context, observation Observation)
}

type Observation struct {
	Type       ObservationType
	Operation  string
	SessionID  string
	RunID      string
	ProviderID string
	ToolName   string
	Attempt    int
	DurationMS int64
	Retryable  bool
	ErrorKind  ErrorKind
	Error      error
}
