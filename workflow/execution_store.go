package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// ExecutionFilter limits execution history queries. Tenant scope always comes
// from the authenticated principal in context and cannot be overridden here.
// Limit defaults to 100 and is capped at 1000.
type ExecutionFilter struct {
	WorkflowID string
	Status     ExecutionStatus
	Limit      int
}

// ExecutionStore persists workflow checkpoints independently from immutable
// workflow definitions. Update uses optimistic revisions: callers must supply
// the latest returned Revision, and a successful update returns Revision+1.
type ExecutionStore interface {
	Create(context.Context, Execution) (Execution, error)
	Update(context.Context, Execution) (Execution, error)
	Get(context.Context, string) (Execution, bool, error)
	List(context.Context, ExecutionFilter) ([]Execution, error)
}

type DurableExecutionStore interface {
	ExecutionStore
	Durable() bool
}

type ProtectedExecutionStore interface {
	DurableExecutionStore
	Protected() bool
}

// ExecutionTransitionStore commits an execution revision and its audit outbox
// events as one storage transaction. Production executors require this
// contract so durable state can never advance without the corresponding audit
// evidence, or vice versa.
type ExecutionTransitionStore interface {
	ProtectedExecutionStore
	CreateWithEvents(
		context.Context, Execution, []audit.Event,
	) (Execution, error)
	UpdateWithEvents(
		context.Context, Execution, []audit.Event,
	) (Execution, error)
	AtomicWith(audit.Outbox) bool
}

type MemoryExecutionStore struct {
	mu        sync.RWMutex
	items     map[string]Execution
	leases    map[string]memoryExecutionLease
	nextFence int64
	now       func() time.Time
}

func (*MemoryExecutionStore) Durable() bool { return false }

func NewMemoryExecutionStore() *MemoryExecutionStore {
	return &MemoryExecutionStore{
		items: make(map[string]Execution),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *MemoryExecutionStore) Create(ctx context.Context, execution Execution) (Execution, error) {
	if err := ctx.Err(); err != nil {
		return Execution{}, err
	}
	if err := authorizeExecution(ctx, execution); err != nil {
		return Execution{}, err
	}
	if execution.Revision != 0 {
		return Execution{}, core.NewConfigError("new workflow execution revision must be zero")
	}
	execution.Revision = 1
	execution.UpdatedAt = s.now()
	if err := execution.ValidateCheckpoint(); err != nil {
		return Execution{}, err
	}
	cloned, err := cloneExecution(execution)
	if err != nil {
		return Execution{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[execution.ID]; exists {
		return Execution{}, &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow execution already exists",
		}
	}
	s.items[execution.ID] = cloned
	return cloneExecution(cloned)
}

func (s *MemoryExecutionStore) Update(ctx context.Context, execution Execution) (Execution, error) {
	if err := ctx.Err(); err != nil {
		return Execution{}, err
	}
	if err := authorizeExecution(ctx, execution); err != nil {
		return Execution{}, err
	}
	if execution.Revision < 1 {
		return Execution{}, core.NewConfigError("workflow execution update requires a revision")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.items[execution.ID]
	if !exists {
		return Execution{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "workflow execution not found",
		}
	}
	if err := requireExecutionClaim(
		ctx, execution.ID, s.leases[execution.ID], s.now(),
	); err != nil {
		return Execution{}, err
	}
	if current.Principal.TenantID != execution.Principal.TenantID {
		return Execution{}, core.NewPermissionError("workflow execution belongs to another tenant")
	}
	if current.WorkflowID != execution.WorkflowID ||
		current.WorkflowVersion != execution.WorkflowVersion {
		return Execution{}, core.NewConfigError("workflow execution identity cannot change")
	}
	if current.Revision != execution.Revision {
		return Execution{}, &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow execution revision conflict",
		}
	}
	if err := ValidateExecutionUpdate(current, execution); err != nil {
		return Execution{}, err
	}
	execution.Revision++
	execution.UpdatedAt = s.now()
	if err := execution.ValidateCheckpoint(); err != nil {
		return Execution{}, err
	}
	cloned, err := cloneExecution(execution)
	if err != nil {
		return Execution{}, err
	}
	s.items[execution.ID] = cloned
	return cloneExecution(cloned)
}

func (s *MemoryExecutionStore) Get(
	ctx context.Context,
	executionID string,
) (Execution, bool, error) {
	if err := ctx.Err(); err != nil {
		return Execution{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	execution, exists := s.items[executionID]
	if !exists {
		return Execution{}, false, nil
	}
	if err := authorizeExecution(ctx, execution); err != nil {
		return Execution{}, false, err
	}
	cloned, err := cloneExecution(execution)
	return cloned, true, err
}

func (s *MemoryExecutionStore) List(
	ctx context.Context,
	filter ExecutionFilter,
) ([]Execution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	principal, err := executionPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateExecutionFilter(filter); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Execution, 0)
	for _, execution := range s.items {
		if execution.Principal.TenantID != principal.TenantID ||
			filter.WorkflowID != "" && execution.WorkflowID != filter.WorkflowID ||
			filter.Status != "" && execution.Status != filter.Status {
			continue
		}
		cloned, err := cloneExecution(execution)
		if err != nil {
			return nil, err
		}
		output = append(output, cloned)
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].UpdatedAt.Equal(output[j].UpdatedAt) {
			return output[i].ID < output[j].ID
		}
		return output[i].UpdatedAt.After(output[j].UpdatedAt)
	})
	limit := executionListLimit(filter)
	if len(output) > limit {
		output = output[:limit]
	}
	return output, nil
}

func (execution Execution) ValidateCheckpoint() error {
	if execution.ID == "" || execution.WorkflowID == "" || execution.WorkflowVersion < 1 {
		return fmt.Errorf("workflow execution requires id and workflow identity")
	}
	if !execution.Principal.Authenticated() {
		return fmt.Errorf("workflow execution requires an authenticated principal")
	}
	switch execution.Status {
	case ExecutionRunning, ExecutionAwaitingApproval, ExecutionCompleted,
		ExecutionRecoveryRequired, ExecutionFailed, ExecutionCanceled:
	default:
		return fmt.Errorf("invalid workflow execution status %q", execution.Status)
	}
	if execution.StartedAt.IsZero() || execution.Revision < 1 || execution.UpdatedAt.IsZero() {
		return fmt.Errorf("workflow execution requires timestamps and a positive revision")
	}
	if !execution.DeadlineAt.IsZero() &&
		!execution.DeadlineAt.After(execution.StartedAt) {
		return fmt.Errorf("workflow execution deadline must follow its start")
	}
	if execution.NextStep < 0 || execution.NextStep > len(execution.Steps) {
		return fmt.Errorf("workflow execution next step is out of range")
	}
	if execution.Status == ExecutionAwaitingApproval && execution.PendingApprovalID == "" {
		return fmt.Errorf("workflow execution awaiting approval requires an approval id")
	}
	if execution.Status != ExecutionAwaitingApproval && execution.PendingApprovalID != "" {
		return fmt.Errorf("workflow execution has a pending approval in status %q", execution.Status)
	}
	if execution.Status == ExecutionRecoveryRequired &&
		execution.Error == nil {
		return fmt.Errorf(
			"workflow execution requiring recovery must describe the uncertainty",
		)
	}
	terminal := execution.Status == ExecutionCompleted ||
		execution.Status == ExecutionFailed ||
		execution.Status == ExecutionCanceled
	if terminal != !execution.CompletedAt.IsZero() {
		return fmt.Errorf("workflow execution completion timestamp is inconsistent with status")
	}
	seen := make(map[string]struct{}, len(execution.Steps))
	for index, step := range execution.Steps {
		if step.StepID == "" || step.Attempts < 0 {
			return fmt.Errorf("workflow execution step %d is invalid", index)
		}
		if _, exists := seen[step.StepID]; exists {
			return fmt.Errorf("duplicate workflow execution step %q", step.StepID)
		}
		seen[step.StepID] = struct{}{}
		switch step.Status {
		case StepPending, StepRunning, StepSkipped, StepAwaitingApproval,
			StepRecoveryRequired, StepCompleted, StepFailed, StepCanceled:
		default:
			return fmt.Errorf("workflow execution step %q has invalid status %q", step.StepID, step.Status)
		}
	}
	return nil
}

// ValidateExecutionUpdate enforces immutable identity/input fields and
// monotonic execution and step transitions. Custom ExecutionStore
// implementations should call it before accepting an update.
func ValidateExecutionUpdate(previous, next Execution) error {
	if previous.ID != next.ID ||
		previous.WorkflowID != next.WorkflowID ||
		previous.WorkflowVersion != next.WorkflowVersion ||
		previous.Principal.TenantID != next.Principal.TenantID ||
		previous.Principal.ActorID != next.Principal.ActorID ||
		!slices.Equal(previous.Principal.Roles, next.Principal.Roles) ||
		!previous.StartedAt.Equal(next.StartedAt) ||
		!previous.DeadlineAt.Equal(next.DeadlineAt) {
		return core.NewConfigError("workflow execution identity cannot change")
	}
	if !equalJSONMaps(previous.Input, next.Input) ||
		!equalJSONMaps(previous.Context, next.Context) {
		return core.NewConfigError("workflow execution input and context cannot change")
	}
	if previous.Revision != next.Revision {
		return &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow execution revision conflict",
		}
	}
	if executionTerminal(previous.Status) {
		return &core.SkawldError{
			Kind: core.ErrorConflict, Message: "terminal workflow execution is immutable",
		}
	}
	if !allowedExecutionTransition(previous.Status, next.Status) {
		return &core.SkawldError{
			Kind: core.ErrorConflict,
			Message: fmt.Sprintf(
				"invalid workflow execution transition %q to %q",
				previous.Status, next.Status,
			),
		}
	}
	if next.NextStep < previous.NextStep || next.NextStep > previous.NextStep+1 {
		return &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow execution next step is not monotonic",
		}
	}
	if len(previous.Steps) != len(next.Steps) {
		return core.NewConfigError("workflow execution steps cannot change")
	}
	for index := range previous.Steps {
		before, after := previous.Steps[index], next.Steps[index]
		if before.StepID != after.StepID {
			return core.NewConfigError("workflow execution step identity cannot change")
		}
		if after.Attempts < before.Attempts {
			return &core.SkawldError{
				Kind: core.ErrorConflict, Message: "workflow execution attempts cannot decrease",
			}
		}
		if !allowedStepTransition(before.Status, after.Status) {
			return &core.SkawldError{
				Kind: core.ErrorConflict,
				Message: fmt.Sprintf(
					"invalid workflow step %q transition %q to %q",
					before.StepID, before.Status, after.Status,
				),
			}
		}
	}
	return nil
}

func equalJSONMaps(left, right map[string]interface{}) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func executionTerminal(status ExecutionStatus) bool {
	return status == ExecutionCompleted ||
		status == ExecutionFailed ||
		status == ExecutionCanceled
}

func allowedExecutionTransition(previous, next ExecutionStatus) bool {
	if previous == next {
		return true
	}
	switch previous {
	case ExecutionRunning:
		return next == ExecutionAwaitingApproval ||
			next == ExecutionRecoveryRequired ||
			next == ExecutionCompleted ||
			next == ExecutionFailed ||
			next == ExecutionCanceled
	case ExecutionAwaitingApproval:
		return next == ExecutionRunning ||
			next == ExecutionFailed ||
			next == ExecutionCanceled
	case ExecutionRecoveryRequired:
		return next == ExecutionRunning ||
			next == ExecutionCompleted ||
			next == ExecutionFailed ||
			next == ExecutionCanceled
	default:
		return false
	}
}

func allowedStepTransition(previous, next StepStatus) bool {
	if previous == next {
		return true
	}
	switch previous {
	case StepPending:
		return next == StepRunning || next == StepSkipped ||
			next == StepFailed || next == StepCanceled
	case StepRunning:
		return next == StepCompleted ||
			next == StepFailed ||
			next == StepCanceled ||
			next == StepAwaitingApproval ||
			next == StepRecoveryRequired
	case StepAwaitingApproval:
		return next == StepRunning || next == StepFailed ||
			next == StepCanceled
	case StepRecoveryRequired:
		return next == StepRunning ||
			next == StepCompleted ||
			next == StepFailed ||
			next == StepCanceled
	default:
		return false
	}
}

func authorizeExecution(ctx context.Context, execution Execution) error {
	principal, err := executionPrincipal(ctx)
	if err != nil {
		return err
	}
	if execution.Principal.TenantID != principal.TenantID {
		return core.NewPermissionError("workflow execution belongs to another tenant")
	}
	return nil
}

func executionPrincipal(ctx context.Context) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return core.Principal{}, core.NewPermissionError(
			"workflow execution storage requires an authenticated tenant",
		)
	}
	return principal, nil
}

func validateExecutionFilter(filter ExecutionFilter) error {
	if filter.Limit < 0 || filter.Limit > 1000 {
		return core.NewConfigError("workflow execution list limit must be between 0 and 1000")
	}
	switch filter.Status {
	case "", ExecutionRunning, ExecutionAwaitingApproval, ExecutionCompleted,
		ExecutionRecoveryRequired, ExecutionFailed, ExecutionCanceled:
		return nil
	default:
		return core.NewConfigError("workflow execution list status is invalid")
	}
}

func executionListLimit(filter ExecutionFilter) int {
	if filter.Limit == 0 {
		return 100
	}
	return filter.Limit
}

func cloneExecution(execution Execution) (Execution, error) {
	raw, err := json.Marshal(execution)
	if err != nil {
		return Execution{}, fmt.Errorf("clone workflow execution: %w", err)
	}
	var cloned Execution
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return Execution{}, fmt.Errorf("clone workflow execution: %w", err)
	}
	return cloned, nil
}
