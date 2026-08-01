package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type RecoveryDecision string

const (
	RecoveryConfirmedCompleted RecoveryDecision = "confirmed_completed"
	RecoveryConfirmedNotRun    RecoveryDecision = "confirmed_not_executed"
	RecoveryCompensated        RecoveryDecision = "compensated"
	RecoveryCanceled           RecoveryDecision = "canceled"
)

type RecoveryRequest struct {
	Decision     RecoveryDecision
	StepID       string
	Output       interface{}
	Reason       string
	EvidenceCode string
}

// Recover resolves an explicitly uncertain running tool checkpoint. It never
// guesses whether an external side effect occurred: an authenticated human or
// application recovery process must provide the decision.
func (e *Executor) Recover(
	ctx context.Context,
	version Version,
	checkpoint Execution,
	request RecoveryRequest,
	principal core.Principal,
) (Execution, error) {
	if !principal.Authenticated() ||
		principal.TenantID != checkpoint.Principal.TenantID ||
		principal.ActorID != checkpoint.Principal.ActorID {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorPermissionDenied,
			Message: "execution recovery requires the initiating authenticated actor",
		}
	}
	if authenticated, ok := core.PrincipalFromContext(ctx); !ok ||
		authenticated.TenantID != principal.TenantID ||
		authenticated.ActorID != principal.ActorID {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorPermissionDenied,
			Message: "execution recovery identity does not match authenticated context",
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
		checkpoint.WorkflowVersion != version.Version {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "recovery checkpoint workflow version mismatch",
		}
	}
	if len(checkpoint.Steps) != len(version.Steps) {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "recovery checkpoint step count does not match the workflow version",
		}
	}
	if (checkpoint.Status != ExecutionRunning &&
		checkpoint.Status != ExecutionRecoveryRequired) ||
		checkpoint.NextStep < 0 ||
		checkpoint.NextStep >= len(version.Steps) {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorConflict,
			Message: "only a running workflow checkpoint can be recovered",
		}
	}
	step := version.Steps[checkpoint.NextStep]
	run := &checkpoint.Steps[checkpoint.NextStep]
	if step.Kind != StepTool ||
		(run.Status != StepRunning &&
			run.Status != StepRecoveryRequired) ||
		request.StepID != step.ID {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorConflict,
			Message: "recovery must identify the current running tool step",
		}
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 4096 {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "execution recovery requires a bounded reason",
		}
	}
	request.EvidenceCode = strings.TrimSpace(request.EvidenceCode)
	if request.EvidenceCode != "" &&
		!validReconciliationIdentifier(request.EvidenceCode) {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "execution recovery evidence code is invalid",
		}
	}
	descriptor, exists, err := e.tools.Describe(ctx, step.Tool.Name)
	if err != nil {
		return checkpoint, err
	}
	if !exists {
		return checkpoint, &ExecutionError{
			Kind: core.ErrorNotFound, StepID: step.ID,
			ToolName: step.Tool.Name, Message: "recovery tool is not registered",
		}
	}
	attributes := map[string]interface{}{
		"decision": string(request.Decision),
		"reason":   request.Reason,
	}
	if request.EvidenceCode != "" {
		attributes["evidence_code"] = request.EvidenceCode
	}
	switch request.Decision {
	case RecoveryConfirmedCompleted:
		if err := e.validateToolOutputSize(
			step.Tool.Name, request.Output,
		); err != nil {
			return checkpoint, err
		}
		if err := ValidateOutput(
			descriptor.OutputSchema, request.Output, step.Tool.Name,
		); err != nil {
			return checkpoint, &ExecutionError{
				Kind: core.ErrorValidation, StepID: step.ID,
				ToolName: step.Tool.Name,
				Message: "recovered output failed trusted schema validation: " +
					err.Error(),
			}
		}
		run.Output = request.Output
		run.Status = StepCompleted
		run.CompletedAt = e.now()
		if checkpoint.State == nil {
			checkpoint.State = make(map[string]interface{})
		}
		checkpoint.State[step.ID] = map[string]interface{}{
			"output": request.Output,
		}
		checkpoint.Status = ExecutionRunning
		checkpoint.Error = nil
		checkpoint.NextStep++
		if err := e.checkpointWithEvents(
			ctx, &checkpoint,
			auditEventSpec{
				eventType: audit.EventExecutionRecovered,
				stepID:    step.ID, toolName: step.Tool.Name,
				outcome:    "confirmed_completed",
				attributes: attributes,
			},
		); err != nil {
			return checkpoint, err
		}
		return e.run(ctx, version, checkpoint)
	case RecoveryConfirmedNotRun:
		checkpoint.Status = ExecutionRunning
		checkpoint.Error = nil
		run.Status = StepRunning
		run.Error = nil
		if err := e.checkpointWithEvents(
			ctx, &checkpoint,
			auditEventSpec{
				eventType: audit.EventExecutionRecovered,
				stepID:    step.ID, toolName: step.Tool.Name,
				outcome:    "confirmed_not_executed",
				attributes: attributes,
			},
		); err != nil {
			return checkpoint, err
		}
		return e.run(ctx, version, checkpoint)
	case RecoveryCompensated, RecoveryCanceled:
		status := ExecutionFailed
		kind := core.ErrorWorkflow
		outcome := "compensated"
		if request.Decision == RecoveryCanceled {
			status = ExecutionCanceled
			kind = core.ErrorAbort
			outcome = "canceled"
		}
		now := e.now()
		checkpoint.Status = status
		checkpoint.CompletedAt = now
		checkpoint.Error = &ExecutionError{
			Kind: kind, StepID: step.ID, ToolName: step.Tool.Name,
			Message: fmt.Sprintf("uncertain execution %s: %s", outcome, request.Reason),
		}
		run.Status = StepFailed
		run.CompletedAt = now
		run.Error = checkpoint.Error
		if err := e.checkpointWithEvents(
			ctx, &checkpoint,
			auditEventSpec{
				eventType: audit.EventExecutionRecovered,
				stepID:    step.ID, toolName: step.Tool.Name,
				outcome: outcome, attributes: attributes,
			},
			auditEventSpec{
				eventType: audit.EventExecutionEnded,
				outcome:   string(status),
			},
		); err != nil {
			return checkpoint, err
		}
		return checkpoint, nil
	default:
		return checkpoint, &ExecutionError{
			Kind: core.ErrorValidation,
			Message: fmt.Sprintf(
				"unsupported execution recovery decision %q",
				request.Decision,
			),
		}
	}
}
