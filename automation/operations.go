package automation

import (
	"context"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func (l *Lifecycle) CancelApproval(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (policy.Approval, error) {
	if !authenticatedIdentity(ctx, principal) {
		return policy.Approval{}, core.NewPermissionError(
			"approval cancellation requires the authenticated actor identity",
		)
	}
	if l.approvals == nil {
		return policy.Approval{}, core.NewConfigError(
			"automation lifecycle approval store is not configured",
		)
	}
	approval, err := l.approvals.Cancel(
		ctx, approvalID, principal, reason,
	)
	if err != nil {
		return policy.Approval{}, err
	}
	if err := l.emitApprovalDecision(ctx, approval, principal); err != nil {
		return approval, err
	}
	return approval, nil
}

// CancelExecution explicitly terminates a persisted non-terminal execution
// through the executor's identity, approval, checkpoint, and audit boundaries.
func (l *Lifecycle) CancelExecution(
	ctx context.Context,
	executionID string,
	principal core.Principal,
	reason string,
) (workflow.Execution, error) {
	if !authenticatedIdentity(ctx, principal) {
		return workflow.Execution{}, core.NewPermissionError(
			"execution cancellation requires the authenticated actor identity",
		)
	}
	if l.executions == nil {
		return workflow.Execution{}, core.NewConfigError(
			"automation lifecycle execution store is not configured",
		)
	}
	checkpoint, exists, err := l.executions.Get(ctx, executionID)
	if err != nil {
		return workflow.Execution{}, err
	}
	if !exists {
		return workflow.Execution{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution checkpoint not found",
		}
	}
	return l.executor.Cancel(ctx, checkpoint, principal, reason)
}

func (l *Lifecycle) ExpireApproval(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (policy.Approval, error) {
	if !authenticatedIdentity(ctx, principal) {
		return policy.Approval{}, core.NewPermissionError(
			"approval expiration requires the authenticated actor identity",
		)
	}
	if l.approvals == nil {
		return policy.Approval{}, core.NewConfigError(
			"automation lifecycle approval store is not configured",
		)
	}
	approval, err := l.approvals.Expire(
		ctx, approvalID, principal, reason,
	)
	if err != nil {
		return policy.Approval{}, err
	}
	if err := l.emitApprovalDecision(ctx, approval, principal); err != nil {
		return approval, err
	}
	return approval, nil
}

func (l *Lifecycle) emitApprovalDecision(
	ctx context.Context,
	approval policy.Approval,
	principal core.Principal,
) error {
	if l.audit == nil {
		return nil
	}
	eventID, err := id.New()
	if err != nil {
		return err
	}
	if err := l.audit.Append(ctx, audit.Event{
		ID: eventID, Type: audit.EventApprovalDecided, Timestamp: l.now(),
		TenantID: principal.TenantID, ActorID: principal.ActorID,
		ExecutionID: approval.ExecutionID, StepID: approval.StepID,
		ToolName: approval.ToolName, ApprovalID: approval.ID,
		Outcome: string(approval.Status),
	}); err != nil {
		return &core.SkawldError{
			Kind:    core.ErrorWorkflow,
			Message: "approval changed but its audit event failed", Cause: err,
		}
	}
	return nil
}

// RecoverExecution loads the exact persisted workflow version and delegates to
// the executor's explicit uncertain-state recovery boundary.
func (l *Lifecycle) RecoverExecution(
	ctx context.Context,
	executionID string,
	request workflow.RecoveryRequest,
	principal core.Principal,
) (workflow.Execution, error) {
	if !authenticatedIdentity(ctx, principal) {
		return workflow.Execution{}, core.NewPermissionError(
			"execution recovery requires the authenticated actor identity",
		)
	}
	if l.executions == nil {
		return workflow.Execution{}, core.NewConfigError(
			"automation lifecycle execution store is not configured",
		)
	}
	checkpoint, exists, err := l.executions.Get(ctx, executionID)
	if err != nil {
		return workflow.Execution{}, err
	}
	if !exists {
		return workflow.Execution{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution checkpoint not found",
		}
	}
	version, exists, err := l.workflows.Get(
		ctx, checkpoint.WorkflowID, checkpoint.WorkflowVersion,
	)
	if err != nil {
		return checkpoint, err
	}
	if !exists {
		return checkpoint, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution version not found",
		}
	}
	return l.executor.Recover(
		ctx, version, checkpoint, request, principal,
	)
}

// ReconcileExecution resolves uncertain execution state through trusted,
// deterministic tool-specific reconciliation code.
func (l *Lifecycle) ReconcileExecution(
	ctx context.Context,
	executionID string,
	principal core.Principal,
) (workflow.Execution, error) {
	if !authenticatedIdentity(ctx, principal) {
		return workflow.Execution{}, core.NewPermissionError(
			"execution reconciliation requires the authenticated actor identity",
		)
	}
	if l.executions == nil {
		return workflow.Execution{}, core.NewConfigError(
			"automation lifecycle execution store is not configured",
		)
	}
	checkpoint, exists, err := l.executions.Get(ctx, executionID)
	if err != nil {
		return workflow.Execution{}, err
	}
	if !exists {
		return workflow.Execution{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution checkpoint not found",
		}
	}
	version, exists, err := l.workflows.Get(
		ctx, checkpoint.WorkflowID, checkpoint.WorkflowVersion,
	)
	if err != nil {
		return checkpoint, err
	}
	if !exists {
		return checkpoint, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution version not found",
		}
	}
	return l.executor.ReconcileRecovery(
		ctx, version, checkpoint, principal,
	)
}
