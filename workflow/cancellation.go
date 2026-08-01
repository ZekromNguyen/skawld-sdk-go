package workflow

import (
	"context"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
)

// Cancel explicitly terminates a non-terminal execution. If an approval is
// pending, its lifecycle record is canceled first through the configured
// approval authorization boundary.
func (e *Executor) Cancel(
	ctx context.Context,
	checkpoint Execution,
	principal core.Principal,
	reason string,
) (Execution, error) {
	if _, err := requireExecutionIdentity(ctx, principal); err != nil ||
		principal.TenantID != checkpoint.Principal.TenantID ||
		principal.ActorID != checkpoint.Principal.ActorID {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorPermissionDenied,
			Message: "execution cancellation requires the initiating authenticated actor",
		}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 4096 ||
		strings.ContainsRune(reason, '\x00') {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "execution cancellation requires a bounded reason",
		}
	}
	if executionTerminal(checkpoint.Status) {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorConflict,
			Message: "terminal workflow execution cannot be canceled",
		}
	}
	runCtx, claimed, release, err := e.acquireExecution(ctx, checkpoint)
	if err != nil {
		return checkpoint, err
	}
	defer release()
	ctx = runCtx
	checkpoint = claimed
	if checkpoint.PendingApprovalID != "" {
		lifecycle, ok := e.approvals.(policy.ApprovalLifecycleStore)
		if !ok {
			return checkpoint, &ExecutionError{
				Kind:    core.ErrorApproval,
				Message: "pending approval store cannot persist cancellation",
			}
		}
		if _, err := lifecycle.Cancel(
			ctx, checkpoint.PendingApprovalID, principal, reason,
		); err != nil {
			return checkpoint, err
		}
		checkpoint.PendingApprovalID = ""
	}
	return e.cancelCheckpoint(
		ctx, checkpoint, core.ErrorAbort, reason, "canceled",
	)
}

func (e *Executor) deadlineElapsed(execution Execution) bool {
	return !execution.DeadlineAt.IsZero() &&
		!e.now().Before(execution.DeadlineAt)
}

func (e *Executor) expireExecution(
	ctx context.Context,
	execution Execution,
	reason string,
) (Execution, error) {
	if execution.PendingApprovalID != "" {
		lifecycle, ok := e.approvals.(policy.ApprovalLifecycleStore)
		if !ok {
			return execution, &ExecutionError{
				Kind:    core.ErrorApproval,
				Message: "pending approval store cannot persist deadline expiration",
			}
		}
		if _, err := lifecycle.Expire(
			ctx, execution.PendingApprovalID,
			execution.Principal, reason,
		); err != nil {
			return execution, err
		}
		execution.PendingApprovalID = ""
	}
	return e.cancelCheckpoint(
		ctx, execution, core.ErrorTimeout, reason, "timed_out",
	)
}

func (e *Executor) cancelCheckpoint(
	ctx context.Context,
	execution Execution,
	kind core.ErrorKind,
	reason string,
	outcome string,
) (Execution, error) {
	now := e.now()
	execution.Status = ExecutionCanceled
	execution.CompletedAt = now
	execution.Error = &ExecutionError{Kind: kind, Message: reason}
	if execution.NextStep >= 0 && execution.NextStep < len(execution.Steps) {
		step := &execution.Steps[execution.NextStep]
		switch step.Status {
		case StepPending, StepRunning, StepAwaitingApproval,
			StepRecoveryRequired:
			step.Status = StepCanceled
			step.CompletedAt = now
			step.Error = execution.Error
		}
	}
	if err := e.checkpointWithEvents(
		ctx, &execution,
		auditEventSpec{
			eventType: audit.EventExecutionCanceled,
			outcome:   outcome,
			attributes: map[string]interface{}{
				"error_kind": string(kind), "reason": reason,
			},
		},
		auditEventSpec{
			eventType: audit.EventExecutionEnded,
			outcome:   outcome,
		},
	); err != nil {
		return execution, err
	}
	return execution, nil
}
