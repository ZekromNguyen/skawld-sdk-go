package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type ReconciliationOutcome string

const (
	ReconciliationCompleted   ReconciliationOutcome = "completed"
	ReconciliationNotExecuted ReconciliationOutcome = "not_executed"
	ReconciliationUnknown     ReconciliationOutcome = "unknown"
	ReconciliationCompensated ReconciliationOutcome = "compensated"
)

type ToolReconciliationRequest struct {
	ExecutionID     string
	WorkflowID      string
	WorkflowVersion int
	StepID          string
	ToolName        string
	Input           map[string]interface{}
	ObservedOutput  interface{}
	IdempotencyKey  string
	Attempts        int
	Principal       core.Principal
}

type ToolReconciliationResult struct {
	Outcome      ReconciliationOutcome
	Output       interface{}
	EvidenceCode string
	Reason       string
}

// ToolReconciler queries authoritative external state. Implementations must be
// deterministic connector code; model output and untrusted content are not
// authoritative reconciliation evidence.
type ToolReconciler interface {
	ReconcileTool(
		context.Context,
		ToolReconciliationRequest,
	) (ToolReconciliationResult, error)
}

// ToolReconcilerCatalog lets the production executor prove during preflight
// that every non-idempotent or unknown side effect has an authoritative
// recovery path before the workflow can perform any action.
type ToolReconcilerCatalog interface {
	ToolReconciler
	CanReconcileTool(string) bool
}

type ReconcilerRegistry struct {
	mu    sync.RWMutex
	items map[string]ToolReconciler
}

func NewReconcilerRegistry() *ReconcilerRegistry {
	return &ReconcilerRegistry{
		items: make(map[string]ToolReconciler),
	}
}

func (r *ReconcilerRegistry) Register(
	toolName string,
	reconciler ToolReconciler,
) error {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || len(toolName) > 256 || reconciler == nil {
		return core.NewConfigError(
			"tool reconciliation registration is invalid",
		)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[toolName]; exists {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "tool reconciler is already registered",
		}
	}
	r.items[toolName] = reconciler
	return nil
}

func (r *ReconcilerRegistry) CanReconcileTool(toolName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.items[strings.TrimSpace(toolName)] != nil
}

func (r *ReconcilerRegistry) ReconcileTool(
	ctx context.Context,
	request ToolReconciliationRequest,
) (ToolReconciliationResult, error) {
	r.mu.RLock()
	reconciler := r.items[request.ToolName]
	r.mu.RUnlock()
	if reconciler == nil {
		return ToolReconciliationResult{}, &core.SkawldError{
			Kind: core.ErrorNotFound,
			Message: fmt.Sprintf(
				"no reconciler is registered for tool %q",
				request.ToolName,
			),
		}
	}
	return reconciler.ReconcileTool(ctx, request)
}

// ReconcileRecovery queries the configured deterministic reconciler and maps
// its authoritative outcome through the same validated recovery transitions
// used by explicit human reconciliation.
func (e *Executor) ReconcileRecovery(
	ctx context.Context,
	version Version,
	checkpoint Execution,
	principal core.Principal,
) (Execution, error) {
	if e.reconciler == nil {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorConfig,
			Message: "workflow tool reconciler is not configured",
		}
	}
	authenticated, err := requireExecutionIdentity(ctx, principal)
	if err != nil ||
		principal.TenantID != checkpoint.Principal.TenantID ||
		principal.ActorID != checkpoint.Principal.ActorID {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorPermissionDenied,
			Message: "execution reconciliation requires the initiating authenticated actor",
		}
	}
	runCtx, claimed, release, err := e.acquireExecution(ctx, checkpoint)
	if err != nil {
		return checkpoint, err
	}
	defer release()
	ctx = runCtx
	checkpoint = claimed
	if checkpoint.WorkflowID != version.Workflow.ID ||
		checkpoint.WorkflowVersion != version.Version ||
		len(checkpoint.Steps) != len(version.Steps) ||
		checkpoint.NextStep < 0 ||
		checkpoint.NextStep >= len(version.Steps) ||
		(checkpoint.Status != ExecutionRunning &&
			checkpoint.Status != ExecutionRecoveryRequired) {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorConflict,
			Message: "execution is not at a recoverable tool checkpoint",
		}
	}
	step := version.Steps[checkpoint.NextStep]
	run := checkpoint.Steps[checkpoint.NextStep]
	if step.Kind != StepTool ||
		(run.Status != StepRunning &&
			run.Status != StepRecoveryRequired) {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorConflict,
			Message: "execution is not at a recoverable tool step",
		}
	}
	idempotencyKey := ""
	if step.Tool.IdempotencyKey != nil {
		value, exists, resolveErr := resolveValue(
			*step.Tool.IdempotencyKey, checkpoint,
		)
		if resolveErr != nil {
			return checkpoint, resolveErr
		}
		if !exists {
			return checkpoint, &ExecutionError{
				Kind: core.ErrorValidation, StepID: step.ID,
				ToolName: step.Tool.Name,
				Message:  "reconciliation idempotency key is absent",
			}
		}
		idempotencyKey = fmt.Sprint(value)
	}
	result, err := e.reconciler.ReconcileTool(
		ctx, ToolReconciliationRequest{
			ExecutionID:     checkpoint.ID,
			WorkflowID:      checkpoint.WorkflowID,
			WorkflowVersion: checkpoint.WorkflowVersion,
			StepID:          step.ID, ToolName: step.Tool.Name,
			Input: cloneMap(run.Input), ObservedOutput: run.Output,
			IdempotencyKey: idempotencyKey, Attempts: run.Attempts,
			Principal: authenticated,
		},
	)
	if err != nil {
		return checkpoint, err
	}
	result.EvidenceCode = strings.TrimSpace(result.EvidenceCode)
	result.Reason = strings.TrimSpace(result.Reason)
	if !validReconciliationIdentifier(result.EvidenceCode) ||
		result.Reason == "" || len(result.Reason) > 4096 ||
		strings.ContainsRune(result.Reason, '\x00') {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "tool reconciler returned invalid evidence",
		}
	}
	request := RecoveryRequest{
		StepID: step.ID, Output: result.Output,
		Reason: result.Reason, EvidenceCode: result.EvidenceCode,
	}
	switch result.Outcome {
	case ReconciliationCompleted:
		request.Decision = RecoveryConfirmedCompleted
	case ReconciliationNotExecuted:
		request.Decision = RecoveryConfirmedNotRun
	case ReconciliationCompensated:
		request.Decision = RecoveryCompensated
	case ReconciliationUnknown:
		return checkpoint, &ExecutionError{
			Kind: core.ErrorConflict, StepID: step.ID,
			ToolName: step.Tool.Name,
			Message: "tool reconciler could not determine the external outcome: " +
				result.Reason,
		}
	default:
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "tool reconciler returned an invalid outcome",
		}
	}
	return e.Recover(ctx, version, checkpoint, request, principal)
}

func validReconciliationIdentifier(value string) bool {
	if value == "" || len(value) > 256 ||
		strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._:/-", character) {
			continue
		}
		return false
	}
	return true
}

var _ ToolReconciler = (*ReconcilerRegistry)(nil)
var _ ToolReconcilerCatalog = (*ReconcilerRegistry)(nil)
