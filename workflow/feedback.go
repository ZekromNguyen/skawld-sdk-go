package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
)

type FeedbackDisposition string

const (
	FeedbackAccepted   FeedbackDisposition = "accepted"
	FeedbackCorrection FeedbackDisposition = "correction"
	FeedbackFailure    FeedbackDisposition = "failure"
	FeedbackUnsafe     FeedbackDisposition = "unsafe"
)

// FeedbackRequest captures a bounded semantic label, not raw task input, tool
// arguments, or tool output. CorrectedAction is an application action name,
// not executable source.
type FeedbackRequest struct {
	Disposition     FeedbackDisposition
	StepID          string
	ReasonCode      string
	Comment         string
	CorrectedAction string
}

// ExecutionFeedback is an immutable, tenant-scoped label over one terminal
// workflow execution. It is stored separately from execution state so feedback
// cannot rewrite the historical checkpoint.
type ExecutionFeedback struct {
	SchemaVersion   string              `json:"schema_version"`
	ID              string              `json:"id"`
	TenantID        string              `json:"tenant_id"`
	ExecutionID     string              `json:"execution_id"`
	WorkflowID      string              `json:"workflow_id"`
	WorkflowVersion int                 `json:"workflow_version"`
	ExecutionStatus ExecutionStatus     `json:"execution_status"`
	StepID          string              `json:"step_id,omitempty"`
	Disposition     FeedbackDisposition `json:"disposition"`
	ReasonCode      string              `json:"reason_code"`
	Comment         string              `json:"comment,omitempty"`
	CorrectedAction string              `json:"corrected_action,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	CreatedBy       string              `json:"created_by"`
}

func NewExecutionFeedback(
	execution Execution,
	request FeedbackRequest,
	principal core.Principal,
	now time.Time,
) (ExecutionFeedback, error) {
	if !principal.Valid() || principal.TenantID == "" || principal.ActorID == "" {
		return ExecutionFeedback{}, core.NewPermissionError(
			"workflow feedback requires an authenticated actor",
		)
	}
	if execution.Principal.TenantID != principal.TenantID {
		return ExecutionFeedback{}, core.NewPermissionError(
			"workflow execution belongs to another tenant",
		)
	}
	switch execution.Status {
	case ExecutionCompleted, ExecutionFailed, ExecutionCanceled:
	default:
		return ExecutionFeedback{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow feedback requires a terminal execution",
		}
	}
	if request.StepID != "" {
		found := false
		for _, step := range execution.Steps {
			if step.StepID == request.StepID {
				found = true
				break
			}
		}
		if !found {
			return ExecutionFeedback{}, &core.SkawldError{
				Kind:    core.ErrorValidation,
				Message: "workflow feedback references an unknown execution step",
			}
		}
	}
	feedbackID, err := id.New()
	if err != nil {
		return ExecutionFeedback{}, err
	}
	feedback := ExecutionFeedback{
		SchemaVersion: SchemaVersion, ID: feedbackID,
		TenantID: execution.Principal.TenantID, ExecutionID: execution.ID,
		WorkflowID: execution.WorkflowID, WorkflowVersion: execution.WorkflowVersion,
		ExecutionStatus: execution.Status,
		StepID:          strings.TrimSpace(request.StepID), Disposition: request.Disposition,
		ReasonCode:      strings.TrimSpace(request.ReasonCode),
		Comment:         strings.TrimSpace(request.Comment),
		CorrectedAction: strings.TrimSpace(request.CorrectedAction),
		CreatedAt:       now.UTC(), CreatedBy: principal.ActorID,
	}
	if err := feedback.Validate(); err != nil {
		return ExecutionFeedback{}, err
	}
	return feedback, nil
}

func (f ExecutionFeedback) Validate() error {
	if f.SchemaVersion != SchemaVersion || f.ID == "" || f.TenantID == "" ||
		f.ExecutionID == "" || f.WorkflowID == "" || f.WorkflowVersion < 1 ||
		f.CreatedBy == "" || f.CreatedAt.IsZero() {
		return fmt.Errorf("workflow feedback has invalid identity")
	}
	switch f.Disposition {
	case FeedbackAccepted, FeedbackCorrection, FeedbackFailure, FeedbackUnsafe:
	default:
		return fmt.Errorf("workflow feedback has invalid disposition %q", f.Disposition)
	}
	switch f.ExecutionStatus {
	case ExecutionCompleted, ExecutionFailed, ExecutionCanceled:
	default:
		return fmt.Errorf("workflow feedback execution status is not terminal")
	}
	if f.Disposition == FeedbackAccepted && f.ExecutionStatus != ExecutionCompleted {
		return fmt.Errorf("accepted feedback requires a completed execution")
	}
	if f.Disposition == FeedbackFailure &&
		f.ExecutionStatus != ExecutionFailed && f.ExecutionStatus != ExecutionCanceled {
		return fmt.Errorf("failure feedback requires a failed or canceled execution")
	}
	if !validFeedbackIdentifier(f.ReasonCode, 128) {
		return fmt.Errorf("workflow feedback reason code is invalid")
	}
	if f.StepID != "" && !validFeedbackIdentifier(f.StepID, 128) {
		return fmt.Errorf("workflow feedback step id is invalid")
	}
	if len(f.Comment) > 4096 || strings.ContainsRune(f.Comment, '\x00') {
		return fmt.Errorf("workflow feedback comment is invalid")
	}
	if f.Disposition == FeedbackCorrection {
		if !validFeedbackIdentifier(f.CorrectedAction, 256) {
			return fmt.Errorf("corrective feedback requires a semantic corrected action")
		}
	} else if f.CorrectedAction != "" {
		return fmt.Errorf("corrected action is only valid for corrective feedback")
	}
	return nil
}

type FeedbackFilter struct {
	WorkflowID      string
	WorkflowVersion int
	ExecutionID     string
	Disposition     FeedbackDisposition
	Limit           int
}

type FeedbackStore interface {
	Save(context.Context, ExecutionFeedback) error
	Get(context.Context, string) (ExecutionFeedback, bool, error)
	List(context.Context, FeedbackFilter) ([]ExecutionFeedback, error)
}

type MemoryFeedbackStore struct {
	mu    sync.RWMutex
	items map[string]ExecutionFeedback
}

func NewMemoryFeedbackStore() *MemoryFeedbackStore {
	return &MemoryFeedbackStore{items: make(map[string]ExecutionFeedback)}
}

func (s *MemoryFeedbackStore) Save(
	ctx context.Context,
	feedback ExecutionFeedback,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := feedback.Validate(); err != nil {
		return err
	}
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID != feedback.TenantID ||
		principal.ActorID != feedback.CreatedBy {
		return core.NewPermissionError(
			"workflow feedback identity does not match authenticated actor",
		)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[feedback.ID]; exists {
		return &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow feedback already exists",
		}
	}
	s.items[feedback.ID] = feedback
	return nil
}

func (s *MemoryFeedbackStore) Get(
	ctx context.Context,
	feedbackID string,
) (ExecutionFeedback, bool, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionFeedback{}, false, err
	}
	principal, err := feedbackPrincipal(ctx)
	if err != nil {
		return ExecutionFeedback{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	feedback, exists := s.items[feedbackID]
	if !exists || feedback.TenantID != principal.TenantID {
		return ExecutionFeedback{}, false, nil
	}
	return feedback, true, nil
}

func (s *MemoryFeedbackStore) List(
	ctx context.Context,
	filter FeedbackFilter,
) ([]ExecutionFeedback, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	principal, err := feedbackPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]ExecutionFeedback, 0)
	for _, feedback := range s.items {
		if feedback.TenantID != principal.TenantID ||
			filter.WorkflowID != "" && feedback.WorkflowID != filter.WorkflowID ||
			filter.WorkflowVersion > 0 && feedback.WorkflowVersion != filter.WorkflowVersion ||
			filter.ExecutionID != "" && feedback.ExecutionID != filter.ExecutionID ||
			filter.Disposition != "" && feedback.Disposition != filter.Disposition {
			continue
		}
		output = append(output, feedback)
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].CreatedAt.Equal(output[j].CreatedAt) {
			return output[i].ID < output[j].ID
		}
		return output[i].CreatedAt.After(output[j].CreatedAt)
	})
	if limit := feedbackLimit(filter.Limit); len(output) > limit {
		output = output[:limit]
	}
	return output, nil
}

func (filter FeedbackFilter) Validate() error {
	if filter.WorkflowVersion < 0 || filter.Limit < 0 || filter.Limit > 1000 {
		return core.NewConfigError("workflow feedback filter is invalid")
	}
	switch filter.Disposition {
	case "", FeedbackAccepted, FeedbackCorrection, FeedbackFailure, FeedbackUnsafe:
	default:
		return core.NewConfigError("workflow feedback filter disposition is invalid")
	}
	return nil
}

func feedbackLimit(limit int) int {
	if limit == 0 {
		return 100
	}
	return limit
}

func feedbackPrincipal(ctx context.Context) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return core.Principal{}, core.NewPermissionError(
			"workflow feedback storage requires an authenticated tenant",
		)
	}
	return principal, nil
}

func validFeedbackIdentifier(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
