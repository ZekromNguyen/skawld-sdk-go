// Package audit defines durable, structured records for consequential SDK
// actions. Audit events intentionally carry hashes and summaries by default;
// applications decide whether encrypted raw payload storage is appropriate.
package audit

import (
	"context"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type EventType string

const (
	EventExecutionStarted   EventType = "execution.started"
	EventStepStarted        EventType = "step.started"
	EventPolicyEvaluated    EventType = "policy.evaluated"
	EventApprovalRequested  EventType = "approval.requested"
	EventApprovalDecided    EventType = "approval.decided"
	EventToolCalled         EventType = "tool.called"
	EventToolCompleted      EventType = "tool.completed"
	EventStepCompleted      EventType = "step.completed"
	EventStepFailed         EventType = "step.failed"
	EventExecutionEnded     EventType = "execution.ended"
	EventWorkflowReviewed   EventType = "workflow.reviewed"
	EventWorkflowPublished  EventType = "workflow.published"
	EventFeedbackRecorded   EventType = "workflow.feedback_recorded"
	EventRouteChanged       EventType = "workflow.route_changed"
	EventExecutionRecovered EventType = "execution.recovered"
	EventRecoveryRequired   EventType = "execution.recovery_required"
	EventExecutionCanceled  EventType = "execution.canceled"
)

type Event struct {
	ID              string                 `json:"id"`
	Type            EventType              `json:"type"`
	Timestamp       time.Time              `json:"timestamp"`
	TenantID        string                 `json:"tenant_id,omitempty"`
	ActorID         string                 `json:"actor_id,omitempty"`
	ExecutionID     string                 `json:"execution_id,omitempty"`
	WorkflowID      string                 `json:"workflow_id,omitempty"`
	WorkflowVersion int                    `json:"workflow_version,omitempty"`
	StepID          string                 `json:"step_id,omitempty"`
	ToolName        string                 `json:"tool_name,omitempty"`
	ToolCallID      string                 `json:"tool_call_id,omitempty"`
	ApprovalID      string                 `json:"approval_id,omitempty"`
	Model           core.ModelID           `json:"model,omitempty"`
	Reason          string                 `json:"reason,omitempty"`
	Outcome         string                 `json:"outcome,omitempty"`
	InputHash       string                 `json:"input_hash,omitempty"`
	OutputHash      string                 `json:"output_hash,omitempty"`
	Attributes      map[string]interface{} `json:"attributes,omitempty"`
}

type Sink interface {
	Append(context.Context, Event) error
}

type Reader interface {
	List(context.Context, string) ([]Event, error)
}

type Store interface {
	Sink
	Reader
}

// MemoryStore is useful for tests and local embeddings. Production deployments
// should provide a durable append-only Store.
type MemoryStore struct {
	mu     sync.RWMutex
	events []Event
	ids    map[string]struct{}
}

func (s *MemoryStore) Append(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, _ := core.PrincipalFromContext(ctx)
	if event.TenantID != "" && event.TenantID != principal.TenantID {
		return core.NewPermissionError("audit event belongs to another tenant")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids == nil {
		s.ids = make(map[string]struct{})
	}
	if _, exists := s.ids[event.ID]; exists && event.ID != "" {
		return nil
	}
	event.Attributes = cloneMap(event.Attributes)
	s.events = append(s.events, event)
	if event.ID != "" {
		s.ids[event.ID] = struct{}{}
	}
	return nil
}

func (s *MemoryStore) List(ctx context.Context, executionID string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, 0, len(s.events))
	for _, event := range s.events {
		if executionID != "" && event.ExecutionID != executionID {
			continue
		}
		principal, _ := core.PrincipalFromContext(ctx)
		if event.TenantID != "" && event.TenantID != principal.TenantID {
			continue
		}
		event.Attributes = cloneMap(event.Attributes)
		out = append(out, event)
	}
	return out, nil
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
