package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
)

type ToolResult struct {
	Output    interface{}
	Retryable bool
}

// ToolRunner is the single gateway to real-world capabilities. Implementations
// must validate arguments against the tool input schema before executing.
type ToolRunner interface {
	Describe(context.Context, string) (core.ToolDescriptor, bool, error)
	Execute(context.Context, string, map[string]interface{}, string) (ToolResult, error)
}

type ExecutorOptions struct {
	Tools       ToolRunner
	Policy      policy.Evaluator
	Approvals   policy.ApprovalStore
	Audit       audit.Sink
	AuditOutbox audit.Outbox
	Executions  ExecutionStore
	Reconciler  ToolReconciler
	ApprovalTTL time.Duration
	// ExecutionTimeout is a workflow-wide deadline. Zero keeps the legacy
	// unbounded behavior; individual step timeouts still apply.
	ExecutionTimeout       time.Duration
	WorkerID               string
	ExecutionLeaseDuration time.Duration
	RequireExecutionLease  bool
	Now                    func() time.Time
	Production             *ExecutorProductionOptions
}

// ExecutorLimits bounds workflow definitions and durable checkpoint payloads.
type ExecutorLimits struct {
	MaxSteps           int
	MaxToolOutputBytes int
	MaxCheckpointBytes int
}

// ExecutorProductionOptions enables fail-closed runtime configuration.
type ExecutorProductionOptions struct {
	Limits ExecutorLimits
}

type Executor struct {
	tools            ToolRunner
	policy           policy.Evaluator
	approvals        policy.ApprovalStore
	audit            audit.Sink
	executions       ExecutionStore
	transitions      ExecutionTransitionStore
	reconciler       ToolReconciler
	approvalTTL      time.Duration
	executionTimeout time.Duration
	workerID         string
	leaseDuration    time.Duration
	requireLease     bool
	now              func() time.Time
	limits           ExecutorLimits
	production       bool
}

func NewExecutor(options ExecutorOptions) (*Executor, error) {
	if err := validateProductionExecutorOptions(options); err != nil {
		return nil, err
	}
	if options.Tools == nil {
		return nil, core.NewConfigError("workflow executor requires a tool runner")
	}
	if options.Policy == nil {
		var err error
		options.Policy, err = policy.NewRolePolicy(policy.RolePolicyOptions{})
		if err != nil {
			return nil, err
		}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.ApprovalTTL < 0 {
		return nil, core.NewConfigError(
			"workflow approval TTL must not be negative",
		)
	}
	if options.ExecutionTimeout < 0 {
		return nil, core.NewConfigError(
			"workflow execution timeout must not be negative",
		)
	}
	if options.RequireExecutionLease && options.WorkerID == "" {
		return nil, core.NewConfigError(
			"required workflow execution leasing needs a worker id",
		)
	}
	if options.WorkerID != "" {
		if _, ok := options.Executions.(ExecutionLeaseStore); !ok {
			return nil, core.NewConfigError(
				"workflow execution worker requires a leased execution store",
			)
		}
		if options.ExecutionLeaseDuration == 0 {
			options.ExecutionLeaseDuration = 30 * time.Second
		}
		if err := validateLeaseRequest(
			options.WorkerID, options.ExecutionLeaseDuration,
		); err != nil {
			return nil, err
		}
	} else if options.ExecutionLeaseDuration != 0 {
		return nil, core.NewConfigError(
			"workflow execution lease duration requires a worker id",
		)
	}
	if options.AuditOutbox != nil {
		dispatcher, err := audit.NewDispatcher(options.AuditOutbox, options.Audit)
		if err != nil {
			return nil, err
		}
		options.Audit = dispatcher
	}
	var transitions ExecutionTransitionStore
	if options.Production != nil {
		transitions, _ = options.Executions.(ExecutionTransitionStore)
	}
	return &Executor{
		tools: options.Tools, policy: options.Policy, approvals: options.Approvals,
		audit: options.Audit, executions: options.Executions,
		transitions:      transitions,
		reconciler:       options.Reconciler,
		approvalTTL:      options.ApprovalTTL,
		executionTimeout: options.ExecutionTimeout,
		workerID:         options.WorkerID,
		leaseDuration:    options.ExecutionLeaseDuration,
		requireLease:     options.RequireExecutionLease,
		now:              options.Now,
		limits:           options.productionLimits(),
		production:       options.Production != nil,
	}, nil
}

func (options ExecutorOptions) productionLimits() ExecutorLimits {
	if options.Production == nil {
		return ExecutorLimits{}
	}
	return options.Production.Limits
}

// NewProductionExecutor makes the fail-closed workflow profile explicit.
func NewProductionExecutor(options ExecutorOptions) (*Executor, error) {
	if options.Production == nil {
		return nil, core.NewConfigError(
			"production workflow executor requires production options",
		)
	}
	return NewExecutor(options)
}

func validateProductionExecutorOptions(options ExecutorOptions) error {
	if options.Production == nil {
		return nil
	}
	switch {
	case options.Policy == nil:
		return core.NewConfigError(
			"production workflow executor requires an explicit policy",
		)
	case options.Approvals == nil:
		return core.NewConfigError(
			"production workflow executor requires a durable approval store",
		)
	case options.AuditOutbox == nil:
		return core.NewConfigError(
			"production workflow executor requires a durable audit outbox",
		)
	case options.Executions == nil:
		return core.NewConfigError(
			"production workflow executor requires a durable execution store",
		)
	case options.Reconciler == nil:
		return core.NewConfigError(
			"production workflow executor requires a side-effect reconciler",
		)
	case !options.RequireExecutionLease:
		return core.NewConfigError(
			"production workflow executor requires execution leasing",
		)
	case options.WorkerID == "":
		return core.NewConfigError(
			"production workflow executor requires a worker id",
		)
	case options.ApprovalTTL <= 0:
		return core.NewConfigError(
			"production workflow executor requires a positive approval TTL",
		)
	case options.ExecutionTimeout <= 0:
		return core.NewConfigError(
			"production workflow executor requires a positive execution timeout",
		)
	}
	capabilityPolicy, ok := options.Policy.(policy.CapabilityEvaluator)
	if !ok || !capabilityPolicy.EnforcesCapabilities() {
		return core.NewConfigError(
			"production workflow executor requires a role/capability authorization policy",
		)
	}
	durableApprovals, ok := options.Approvals.(policy.DurableApprovalStore)
	if !ok || !durableApprovals.Durable() {
		return core.NewConfigError(
			"production workflow executor requires a durable approval store",
		)
	}
	protectedApprovals, ok := options.Approvals.(policy.ProtectedApprovalStore)
	if !ok || !protectedApprovals.Protected() {
		return core.NewConfigError(
			"production workflow executor requires a protected approval store",
		)
	}
	durableExecutions, ok := options.Executions.(DurableExecutionStore)
	if !ok || !durableExecutions.Durable() {
		return core.NewConfigError(
			"production workflow executor requires a durable execution store",
		)
	}
	protectedExecutions, ok := options.Executions.(ProtectedExecutionStore)
	if !ok || !protectedExecutions.Protected() {
		return core.NewConfigError(
			"production workflow executor requires a protected execution store",
		)
	}
	atomicExecutions, ok := options.Executions.(ExecutionTransitionStore)
	if !ok || !atomicExecutions.AtomicWith(options.AuditOutbox) {
		return core.NewConfigError(
			"production workflow executor requires atomic execution and audit-outbox transitions",
		)
	}
	durableOutbox, ok := options.AuditOutbox.(audit.DurableOutbox)
	if !ok || !durableOutbox.Durable() {
		return core.NewConfigError(
			"production workflow executor requires a durable audit outbox",
		)
	}
	protectedOutbox, ok := options.AuditOutbox.(audit.ProtectedOutbox)
	if !ok || !protectedOutbox.Protected() {
		return core.NewConfigError(
			"production workflow executor requires a protected audit outbox",
		)
	}
	limits := options.Production.Limits
	switch {
	case limits.MaxSteps <= 0:
		return core.NewConfigError(
			"production workflow executor requires a positive step limit",
		)
	case limits.MaxToolOutputBytes <= 0:
		return core.NewConfigError(
			"production workflow executor requires a positive tool-output byte limit",
		)
	case limits.MaxCheckpointBytes <= 0:
		return core.NewConfigError(
			"production workflow executor requires a positive checkpoint byte limit",
		)
	}
	return nil
}

func (e *Executor) Execute(ctx context.Context, version Version, input, workflowContext map[string]interface{}, principal core.Principal) (Execution, error) {
	authenticated, err := requireExecutionIdentity(ctx, principal)
	if err != nil {
		return Execution{}, &ExecutionError{
			Kind:    core.ErrorPermissionDenied,
			Message: err.Error(),
		}
	}
	// Store trusted claims from the authenticated context. In particular,
	// callers cannot add roles through the method argument.
	principal = authenticated
	if version.Status != VersionPublished {
		return Execution{}, &ExecutionError{Kind: core.ErrorValidation, Message: "only published workflow versions can execute"}
	}
	if e.production && len(version.Steps) > e.limits.MaxSteps {
		return Execution{}, &ExecutionError{
			Kind: core.ErrorValidation,
			Message: fmt.Sprintf(
				"workflow has %d steps; production limit is %d",
				len(version.Steps), e.limits.MaxSteps,
			),
		}
	}
	if err := version.Validate(); err != nil {
		return Execution{}, &ExecutionError{Kind: core.ErrorValidation, Message: err.Error()}
	}
	if version.Workflow.TenantID != "" && version.Workflow.TenantID != principal.TenantID {
		return Execution{}, &ExecutionError{Kind: core.ErrorPermissionDenied, Message: "workflow belongs to another tenant"}
	}
	if err := e.validateToolCatalog(ctx, version); err != nil {
		return Execution{}, &ExecutionError{
			Kind: core.ErrorValidation, Message: "workflow tool catalog preflight failed: " + err.Error(),
		}
	}
	if err := e.validateReferences(ctx, version); err != nil {
		return Execution{}, &ExecutionError{
			Kind: core.ErrorValidation, Message: "workflow reference preflight failed: " + err.Error(),
		}
	}
	if err := ValidateInputs(version, input, workflowContext); err != nil {
		return Execution{}, &ExecutionError{
			Kind: core.ErrorValidation, Message: "workflow input preflight failed: " + err.Error(),
		}
	}
	startedAt := e.now()
	executionID, err := id.New()
	if err != nil {
		return Execution{}, err
	}
	execution := Execution{
		ID:              executionID,
		WorkflowID:      version.Workflow.ID,
		WorkflowVersion: version.Version,
		Principal:       principal,
		Status:          ExecutionRunning,
		Input:           cloneMap(input),
		Context:         cloneMap(workflowContext),
		State:           make(map[string]interface{}),
		Steps:           make([]StepExecution, len(version.Steps)),
		Approvals:       make(map[string]string),
		StartedAt:       startedAt,
	}
	if e.executionTimeout > 0 {
		execution.DeadlineAt = startedAt.Add(e.executionTimeout)
	}
	for index, step := range version.Steps {
		execution.Steps[index] = StepExecution{StepID: step.ID, Status: StepPending}
	}
	if err := e.validateCheckpointSize(execution); err != nil {
		return Execution{}, err
	}
	if err := e.createWithEvents(
		ctx, &execution,
		auditEventSpec{
			eventType: audit.EventExecutionStarted,
			outcome:   "started",
		},
	); err != nil {
		return Execution{}, err
	}
	runCtx, claimed, release, err := e.acquireExecution(ctx, execution)
	if err != nil {
		return execution, err
	}
	defer release()
	ctx = runCtx
	execution = claimed
	return e.run(ctx, version, execution)
}

// Resume continues an execution after its durable pending approval has been
// decided. The supplied execution is treated as a checkpoint and is copied.
func (e *Executor) Resume(ctx context.Context, version Version, checkpoint Execution) (Execution, error) {
	if _, err := requireExecutionIdentity(ctx, checkpoint.Principal); err != nil {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorPermissionDenied,
			Message: err.Error(),
		}
	}
	if checkpoint.WorkflowID != version.Workflow.ID || checkpoint.WorkflowVersion != version.Version {
		return checkpoint, &ExecutionError{Kind: core.ErrorValidation, Message: "checkpoint workflow version mismatch"}
	}
	runCtx, claimed, release, err := e.acquireExecution(ctx, checkpoint)
	if err != nil {
		return checkpoint, err
	}
	defer release()
	ctx = runCtx
	checkpoint = claimed
	if err := e.validateToolCatalog(ctx, version); err != nil {
		return checkpoint, &ExecutionError{
			Kind: core.ErrorValidation, Message: "workflow tool catalog preflight failed: " + err.Error(),
		}
	}
	if err := e.validateReferences(ctx, version); err != nil {
		return checkpoint, &ExecutionError{
			Kind: core.ErrorValidation, Message: "workflow reference preflight failed: " + err.Error(),
		}
	}
	if checkpoint.Status != ExecutionAwaitingApproval || checkpoint.PendingApprovalID == "" {
		return checkpoint, &ExecutionError{Kind: core.ErrorConflict, Message: "execution is not awaiting approval"}
	}
	if e.deadlineElapsed(checkpoint) {
		return e.expireExecution(ctx, checkpoint, "workflow execution deadline elapsed")
	}
	if e.approvals == nil {
		return checkpoint, &ExecutionError{Kind: core.ErrorConfig, Message: "approval store is not configured"}
	}
	approval, ok, err := e.approvals.Get(ctx, checkpoint.PendingApprovalID)
	if err != nil {
		return checkpoint, err
	}
	if !ok {
		return checkpoint, &ExecutionError{Kind: core.ErrorNotFound, Message: "pending approval not found"}
	}
	switch approval.Status {
	case policy.ApprovalPending:
		if approval.ExpiresAt.IsZero() ||
			e.now().Before(approval.ExpiresAt) {
			return checkpoint, nil
		}
		lifecycle, ok := e.approvals.(policy.ApprovalLifecycleStore)
		if !ok {
			return checkpoint, &ExecutionError{
				Kind:    core.ErrorApproval,
				Message: "approval expired but its store cannot persist expiration",
			}
		}
		approval, err = lifecycle.Expire(
			ctx, approval.ID, checkpoint.Principal,
			"approval deadline elapsed",
		)
		if err != nil {
			return checkpoint, err
		}
		fallthrough
	case policy.ApprovalRejected, policy.ApprovalExpired,
		policy.ApprovalCanceled:
		checkpoint.PendingApprovalID = ""
		checkpoint.Status = ExecutionFailed
		checkpoint.CompletedAt = e.now()
		checkpoint.Error = &ExecutionError{
			Kind: core.ErrorApproval, StepID: version.Steps[checkpoint.NextStep].ID,
			Message: "workflow approval was " + string(approval.Status),
		}
		checkpoint.Steps[checkpoint.NextStep].Status = StepFailed
		checkpoint.Steps[checkpoint.NextStep].Error = checkpoint.Error
		checkpoint.Steps[checkpoint.NextStep].CompletedAt = checkpoint.CompletedAt
		if err := e.checkpointWithEvents(
			ctx, &checkpoint,
			auditEventSpec{
				eventType:  audit.EventApprovalDecided,
				stepID:     version.Steps[checkpoint.NextStep].ID,
				approvalID: approval.ID, outcome: string(approval.Status),
			},
			auditEventSpec{
				eventType: audit.EventExecutionEnded,
				outcome:   "failed",
			},
		); err != nil {
			return checkpoint, err
		}
		return checkpoint, nil
	case policy.ApprovalGranted:
		stepID := version.Steps[checkpoint.NextStep].ID
		if checkpoint.Approvals == nil {
			checkpoint.Approvals = make(map[string]string)
		}
		checkpoint.Approvals[stepID] = approval.ID
		checkpoint.PendingApprovalID = ""
		checkpoint.Status = ExecutionRunning
		if err := e.checkpointWithEvents(
			ctx, &checkpoint,
			auditEventSpec{
				eventType: audit.EventApprovalDecided,
				stepID:    stepID, approvalID: approval.ID,
				outcome: "granted",
			},
		); err != nil {
			return checkpoint, err
		}
	default:
		return checkpoint, &ExecutionError{Kind: core.ErrorApproval, Message: "invalid approval status"}
	}
	return e.run(ctx, version, checkpoint)
}

func requireExecutionIdentity(
	ctx context.Context,
	principal core.Principal,
) (core.Principal, error) {
	if !principal.Authenticated() {
		return core.Principal{}, fmt.Errorf(
			"workflow execution requires authenticated tenant and actor identities",
		)
	}
	authenticated, exists := core.PrincipalFromContext(ctx)
	if !exists || !authenticated.Authenticated() {
		return core.Principal{}, fmt.Errorf(
			"workflow execution requires an authenticated context identity",
		)
	}
	if authenticated.TenantID != principal.TenantID ||
		authenticated.ActorID != principal.ActorID {
		return core.Principal{}, fmt.Errorf(
			"workflow execution identity does not match authenticated context",
		)
	}
	return authenticated, nil
}

func (e *Executor) validateToolCatalog(ctx context.Context, version Version) error {
	if version.ToolCatalogDigest == "" {
		return nil
	}
	fingerprinter, ok := e.tools.(ToolCatalogFingerprinter)
	if !ok {
		return fmt.Errorf("tool runner cannot verify the workflow tool catalog")
	}
	current, err := fingerprinter.ToolCatalogFingerprint(ctx, ReferencedToolNames(version))
	if err != nil {
		return err
	}
	if current != version.ToolCatalogDigest {
		return fmt.Errorf("tool contracts changed after workflow compilation")
	}
	return nil
}

func (e *Executor) validateReferences(ctx context.Context, version Version) error {
	outputs := make(map[string]map[string]interface{})
	for _, step := range version.Steps {
		if step.Tool == nil {
			continue
		}
		descriptor, exists, err := e.tools.Describe(ctx, step.Tool.Name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("workflow tool %q is not registered", step.Tool.Name)
		}
		if e.production && len(descriptor.Permissions) == 0 {
			return fmt.Errorf(
				"production workflow tool %q requires at least one capability",
				step.Tool.Name,
			)
		}
		if e.production && len(descriptor.OutputSchema) == 0 {
			return fmt.Errorf(
				"production workflow tool %q requires a trusted output schema",
				step.Tool.Name,
			)
		}
		if e.production &&
			(descriptor.SideEffect == core.SideEffectNonIdempotent ||
				descriptor.SideEffect == core.SideEffectUnknown) &&
			!workflowCallUsesIdempotency(descriptor, step.Tool) {
			catalog, ok := e.reconciler.(ToolReconcilerCatalog)
			if !ok || !catalog.CanReconcileTool(step.Tool.Name) {
				return fmt.Errorf(
					"production workflow tool %q requires an authoritative reconciler or an idempotency key",
					step.Tool.Name,
				)
			}
		}
		if err := validateContractSchema(
			"tool_output_schema."+step.Tool.Name,
			descriptor.OutputSchema, false, 0,
		); err != nil {
			return err
		}
		outputs[step.Tool.Name] = descriptor.OutputSchema
	}
	return ValidateReferences(version, outputs)
}

func workflowCallUsesIdempotency(
	descriptor core.ToolDescriptor,
	call *ToolCall,
) bool {
	if call == nil || call.IdempotencyKey == nil {
		return false
	}
	return descriptor.Idempotency == core.IdempotencyOptional ||
		descriptor.Idempotency == core.IdempotencyRequired
}

func (e *Executor) run(ctx context.Context, version Version, execution Execution) (Execution, error) {
	for execution.NextStep < len(version.Steps) {
		if e.deadlineElapsed(execution) {
			return e.expireExecution(
				context.WithoutCancel(ctx), execution,
				"workflow execution deadline elapsed",
			)
		}
		if err := ctx.Err(); err != nil {
			return e.cancelCheckpoint(
				context.WithoutCancel(ctx), execution, core.ErrorAbort,
				err.Error(), "canceled",
			)
		}
		index := execution.NextStep
		step := version.Steps[index]
		run := &execution.Steps[index]
		if step.When != nil {
			matches, err := evaluateCondition(*step.When, execution)
			if err != nil {
				return e.fail(ctx, execution, run, step, core.ErrorValidation, "", err)
			}
			if !matches {
				run.Status = StepSkipped
				run.CompletedAt = e.now()
				execution.NextStep++
				if err := e.checkpoint(ctx, &execution); err != nil {
					return execution, err
				}
				continue
			}
		}
		run.Status = StepRunning
		if run.StartedAt.IsZero() {
			run.StartedAt = e.now()
		}
		if err := e.checkpointWithEvents(
			ctx, &execution,
			auditEventSpec{
				eventType: audit.EventStepStarted,
				stepID:    step.ID, outcome: "started",
			},
		); err != nil {
			return execution, err
		}
		var completed bool
		var err error
		switch step.Kind {
		case StepTool:
			completed, err = e.runToolStep(ctx, &execution, step, run)
		case StepValidation:
			completed, err = e.runValidationStep(execution, step, run)
		case StepApproval:
			completed, err = e.runApprovalStep(ctx, &execution, step, run)
		default:
			err = fmt.Errorf("unsupported step kind %q", step.Kind)
		}
		if err != nil {
			var execErr *ExecutionError
			if errors.As(err, &execErr) {
				return e.fail(ctx, execution, run, step, execErr.Kind, execErr.ToolName, err)
			}
			return e.fail(ctx, execution, run, step, core.ErrorWorkflow, "", err)
		}
		if !completed {
			if err := e.checkpoint(ctx, &execution); err != nil {
				return execution, err
			}
			return execution, nil
		}
		run.Status = StepCompleted
		run.CompletedAt = e.now()
		execution.NextStep++
		if err := e.checkpointWithEvents(
			ctx, &execution,
			auditEventSpec{
				eventType: audit.EventStepCompleted,
				stepID:    step.ID, outcome: "completed",
			},
		); err != nil {
			return execution, err
		}
	}
	execution.Status = ExecutionCompleted
	execution.CompletedAt = e.now()
	if err := e.checkpointWithEvents(
		ctx, &execution,
		auditEventSpec{
			eventType: audit.EventExecutionEnded,
			outcome:   "completed",
		},
	); err != nil {
		return execution, err
	}
	return execution, nil
}

func (e *Executor) runToolStep(ctx context.Context, execution *Execution, step Step, run *StepExecution) (bool, error) {
	actionPrincipal, err := requireExecutionIdentity(
		ctx, execution.Principal,
	)
	if err != nil {
		return false, &ExecutionError{
			Kind: core.ErrorPermissionDenied, StepID: step.ID,
			ToolName: step.Tool.Name, Message: err.Error(),
		}
	}
	descriptor, ok, err := e.tools.Describe(ctx, step.Tool.Name)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, &ExecutionError{Kind: core.ErrorNotFound, StepID: step.ID, ToolName: step.Tool.Name, Message: "workflow tool is not registered"}
	}
	input := make(map[string]interface{}, len(step.Tool.Arguments))
	for name, value := range step.Tool.Arguments {
		resolved, exists, err := resolveValue(value, *execution)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, fmt.Errorf("argument %q reference %q does not exist", name, value.Ref)
		}
		input[name] = resolved
	}
	run.Input = cloneMap(input)
	decision, err := e.policy.Evaluate(ctx, policy.Action{
		Principal: actionPrincipal, ExecutionID: execution.ID, WorkflowID: execution.WorkflowID,
		WorkflowVersion: execution.WorkflowVersion, StepID: step.ID, ToolName: step.Tool.Name,
		Input: input, Descriptor: descriptor, Reason: step.Tool.Reason,
	})
	if err != nil {
		return false, err
	}
	if err := e.emit(ctx, *execution, audit.EventPolicyEvaluated, step.ID, step.Tool.Name, "", string(decision.Kind), map[string]interface{}{"reason": decision.Reason}); err != nil {
		return false, err
	}
	switch decision.Kind {
	case policy.Deny:
		return false, &ExecutionError{Kind: core.ErrorPolicy, StepID: step.ID, ToolName: step.Tool.Name, Message: "policy denied tool call: " + decision.Reason}
	case policy.RequireApproval:
		approved, err := e.requireApproval(ctx, execution, step, descriptor.Risk, step.Tool.Name, decision.Reason, input)
		if err != nil || !approved {
			return false, err
		}
	case policy.Allow:
	default:
		return false, &ExecutionError{Kind: core.ErrorPolicy, StepID: step.ID, Message: "policy returned an invalid decision"}
	}
	idempotencyKey := ""
	if step.Tool.IdempotencyKey != nil {
		resolved, exists, err := resolveValue(*step.Tool.IdempotencyKey, *execution)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, fmt.Errorf("idempotency key reference %q does not exist", step.Tool.IdempotencyKey.Ref)
		}
		idempotencyKey = fmt.Sprint(resolved)
	}
	if descriptor.Idempotency == core.IdempotencyRequired && idempotencyKey == "" {
		return false, &ExecutionError{Kind: core.ErrorValidation, StepID: step.ID, ToolName: step.Tool.Name, Message: "tool requires an idempotency key"}
	}
	maxAttempts := step.Retry.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	if maxAttempts > 1 && descriptor.SideEffect != core.SideEffectNone && descriptor.SideEffect != core.SideEffectIdempotent {
		return false, &ExecutionError{Kind: core.ErrorValidation, StepID: step.ID, ToolName: step.Tool.Name, Message: "automatic retry is forbidden for non-idempotent or unknown side effects"}
	}
	toolCtx := ctx
	cancel := func() {}
	timeout := step.Timeout
	if timeout == 0 {
		timeout = descriptor.Timeout
	}
	if timeout > 0 {
		toolCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	if !execution.DeadlineAt.IsZero() {
		if deadline, ok := toolCtx.Deadline(); !ok ||
			execution.DeadlineAt.Before(deadline) {
			cancel()
			toolCtx, cancel = context.WithDeadline(
				ctx, execution.DeadlineAt,
			)
		}
	}
	defer cancel()
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		run.Attempts = attempt
		if err := e.checkpoint(ctx, execution); err != nil {
			return false, err
		}
		callID, err := id.New()
		if err != nil {
			return false, err
		}
		if err := e.checkpointWithEvents(
			ctx, execution,
			auditEventSpec{
				eventType: audit.EventToolCalled,
				stepID:    step.ID, toolName: step.Tool.Name,
				outcome: "started",
				attributes: map[string]interface{}{
					"tool_call_id": callID,
					"input_hash":   hashValue(input),
					"attempt":      attempt,
				},
			},
		); err != nil {
			return false, err
		}
		result, executeErr := e.tools.Execute(toolCtx, step.Tool.Name, cloneMap(input), idempotencyKey)
		if executeErr == nil {
			if err := e.validateToolOutputSize(
				step.Tool.Name, result.Output,
			); err != nil {
				if descriptor.SideEffect != core.SideEffectNone {
					return e.requireRecovery(
						ctx, execution, step, run, nil,
						"tool completed but its output exceeded the trusted production limit",
					)
				}
				return false, err
			}
			if err := ValidateOutput(
				descriptor.OutputSchema, result.Output, step.Tool.Name,
			); err != nil {
				if descriptor.SideEffect != core.SideEffectNone {
					return e.requireRecovery(
						ctx, execution, step, run, result.Output,
						"tool completed but its output failed trusted schema validation",
					)
				}
				return false, &ExecutionError{
					Kind: core.ErrorValidation, StepID: step.ID,
					ToolName: step.Tool.Name,
					Message: "tool output failed trusted schema validation: " +
						err.Error(),
				}
			}
			candidate, cloneErr := cloneExecution(*execution)
			if cloneErr != nil {
				if descriptor.SideEffect != core.SideEffectNone {
					return e.requireRecovery(
						ctx, execution, step, run, nil,
						"tool completed but its bounded checkpoint could not be constructed",
					)
				}
				return false, cloneErr
			}
			candidate.Steps[execution.NextStep].Output = result.Output
			if candidate.State == nil {
				candidate.State = make(map[string]interface{})
			}
			candidate.State[step.ID] = map[string]interface{}{
				"output": result.Output,
			}
			if err := e.validateCheckpointSize(candidate); err != nil {
				if descriptor.SideEffect != core.SideEffectNone {
					return e.requireRecovery(
						ctx, execution, step, run, nil,
						"tool completed but its result exceeds the durable checkpoint limit",
					)
				}
				return false, err
			}
			run.Output = result.Output
			execution.State[step.ID] = map[string]interface{}{"output": result.Output}
			// Persist the observed result while the step remains running. If
			// the process stops before the completed transition, a restart
			// sees an explicitly recoverable in-flight tool call rather than
			// replaying a possibly applied side effect.
			if err := e.checkpointWithEvents(
				context.WithoutCancel(ctx), execution,
				auditEventSpec{
					eventType: audit.EventToolCompleted,
					stepID:    step.ID, toolName: step.Tool.Name,
					outcome: "completed",
					attributes: map[string]interface{}{
						"tool_call_id": callID,
						"output_hash":  hashValue(result.Output),
						"attempt":      attempt,
					},
				},
			); err != nil {
				if descriptor.SideEffect != core.SideEffectNone {
					return e.requireRecovery(
						ctx, execution, step, run, result.Output,
						"tool completed but its result and audit transition could not be durably recorded",
					)
				}
				return false, err
			}
			return true, nil
		}
		retryable := result.Retryable && attempt < maxAttempts
		if !retryable {
			if descriptor.SideEffect == core.SideEffectUnknown ||
				descriptor.SideEffect == core.SideEffectNonIdempotent {
				return e.requireRecovery(
					ctx, execution, step, run, result.Output,
					"tool failed after a potentially applied external side effect",
				)
			}
			kind := core.ErrorToolExecution
			if errors.Is(toolCtx.Err(), context.DeadlineExceeded) {
				kind = core.ErrorTimeout
			}
			return false, &ExecutionError{
				Kind: kind, StepID: step.ID, ToolName: step.Tool.Name,
				Message: executeErr.Error(), Retryable: result.Retryable,
			}
		}
		if err := waitContext(toolCtx, step.Retry.Backoff); err != nil {
			return false, err
		}
	}
	return false, &ExecutionError{Kind: core.ErrorToolExecution, StepID: step.ID, ToolName: step.Tool.Name, Message: "tool attempts exhausted"}
}

func (e *Executor) requireRecovery(
	ctx context.Context,
	execution *Execution,
	step Step,
	run *StepExecution,
	output interface{},
	reason string,
) (bool, error) {
	run.Output = output
	run.Status = StepRecoveryRequired
	run.Error = &ExecutionError{
		Kind: core.ErrorWorkflow, StepID: step.ID,
		ToolName: step.Tool.Name, Message: reason,
	}
	execution.Status = ExecutionRecoveryRequired
	execution.Error = run.Error
	if err := e.checkpointWithEvents(
		context.WithoutCancel(ctx), execution,
		auditEventSpec{
			eventType: audit.EventRecoveryRequired,
			stepID:    step.ID, toolName: step.Tool.Name,
			outcome:    "recovery_required",
			attributes: map[string]interface{}{"reason": reason},
		},
	); err != nil {
		return false, err
	}
	return false, nil
}

func (e *Executor) runValidationStep(execution Execution, step Step, run *StepExecution) (bool, error) {
	valid, err := evaluateCondition(step.Validation.Condition, execution)
	if err != nil {
		return false, err
	}
	run.Output = valid
	if !valid {
		message := step.Validation.Message
		if message == "" {
			message = "workflow validation failed"
		}
		return false, &ExecutionError{Kind: core.ErrorValidation, StepID: step.ID, Message: message}
	}
	return true, nil
}

func (e *Executor) runApprovalStep(ctx context.Context, execution *Execution, step Step, run *StepExecution) (bool, error) {
	risk := step.Approval.Risk
	if risk == "" {
		risk = core.RiskHigh
	}
	return e.requireApproval(ctx, execution, step, risk, "", step.Approval.Summary, nil)
}

func (e *Executor) requireApproval(ctx context.Context, execution *Execution, step Step, risk core.RiskLevel, toolName, summary string, input map[string]interface{}) (bool, error) {
	if approvalID := execution.Approvals[step.ID]; approvalID != "" {
		return true, nil
	}
	if e.approvals == nil {
		return false, &ExecutionError{Kind: core.ErrorApproval, StepID: step.ID, ToolName: toolName, Message: "approval is required but no approval store is configured"}
	}
	requestedAt := e.now()
	expiresAt := time.Time{}
	if e.approvalTTL > 0 {
		expiresAt = requestedAt.Add(e.approvalTTL)
	}
	if !execution.DeadlineAt.IsZero() &&
		(expiresAt.IsZero() || execution.DeadlineAt.Before(expiresAt)) {
		expiresAt = execution.DeadlineAt
	}
	approval, err := e.approvals.Request(ctx, policy.Approval{
		TenantID: execution.Principal.TenantID, ExecutionID: execution.ID, StepID: step.ID,
		ToolName: toolName, Summary: summary, InputHash: hashValue(input), Risk: risk,
		RequestedAt: requestedAt, ExpiresAt: expiresAt,
	})
	if err != nil {
		return false, err
	}
	execution.PendingApprovalID = approval.ID
	execution.Status = ExecutionAwaitingApproval
	execution.Steps[execution.NextStep].Status = StepAwaitingApproval
	if err := e.checkpointWithEvents(
		ctx, execution,
		auditEventSpec{
			eventType: audit.EventApprovalRequested,
			stepID:    step.ID, toolName: toolName,
			approvalID: approval.ID, outcome: "pending",
		},
	); err != nil {
		return false, err
	}
	return false, nil
}

func (e *Executor) fail(ctx context.Context, execution Execution, run *StepExecution, step Step, kind core.ErrorKind, toolName string, cause error) (Execution, error) {
	execution.Status = ExecutionFailed
	execution.CompletedAt = e.now()
	execution.Error = &ExecutionError{Kind: kind, StepID: step.ID, ToolName: toolName, Message: cause.Error()}
	run.Status = StepFailed
	run.CompletedAt = e.now()
	run.Error = execution.Error
	if err := e.checkpointWithEvents(
		context.WithoutCancel(ctx), &execution,
		auditEventSpec{
			eventType: audit.EventStepFailed,
			stepID:    step.ID, toolName: toolName, outcome: "failed",
			attributes: map[string]interface{}{
				"error_kind": string(kind),
			},
		},
		auditEventSpec{
			eventType: audit.EventExecutionEnded,
			outcome:   "failed",
		},
	); err != nil {
		return execution, err
	}
	return execution, nil
}

func (e *Executor) checkpoint(ctx context.Context, execution *Execution) error {
	if err := e.validateCheckpointSize(*execution); err != nil {
		return err
	}
	if e.executions == nil {
		return nil
	}
	saved, err := e.executions.Update(ctx, *execution)
	if err != nil {
		return fmt.Errorf("persist workflow execution checkpoint: %w", err)
	}
	execution.Revision = saved.Revision
	execution.UpdatedAt = saved.UpdatedAt
	return nil
}

type auditEventSpec struct {
	eventType  audit.EventType
	stepID     string
	toolName   string
	approvalID string
	outcome    string
	attributes map[string]interface{}
}

func (e *Executor) createWithEvents(
	ctx context.Context,
	execution *Execution,
	specs ...auditEventSpec,
) error {
	events, err := e.buildAuditEvents(*execution, specs)
	if err != nil {
		return err
	}
	if e.transitions != nil {
		saved, err := e.transitions.CreateWithEvents(
			ctx, *execution, events,
		)
		if err != nil {
			return err
		}
		*execution = saved
		return nil
	}
	if e.executions != nil {
		saved, err := e.executions.Create(ctx, *execution)
		if err != nil {
			return err
		}
		*execution = saved
	}
	return e.appendAuditEvents(ctx, events)
}

func (e *Executor) checkpointWithEvents(
	ctx context.Context,
	execution *Execution,
	specs ...auditEventSpec,
) error {
	if err := e.validateCheckpointSize(*execution); err != nil {
		return err
	}
	events, err := e.buildAuditEvents(*execution, specs)
	if err != nil {
		return err
	}
	if e.transitions != nil {
		saved, err := e.transitions.UpdateWithEvents(
			ctx, *execution, events,
		)
		if err != nil {
			return fmt.Errorf(
				"persist workflow execution transition: %w", err,
			)
		}
		execution.Revision = saved.Revision
		execution.UpdatedAt = saved.UpdatedAt
		return nil
	}
	if err := e.checkpoint(ctx, execution); err != nil {
		return err
	}
	return e.appendAuditEvents(ctx, events)
}

func (e *Executor) buildAuditEvents(
	execution Execution,
	specs []auditEventSpec,
) ([]audit.Event, error) {
	if e.audit == nil && e.transitions == nil {
		return nil, nil
	}
	events := make([]audit.Event, 0, len(specs))
	for _, spec := range specs {
		eventID, err := id.New()
		if err != nil {
			return nil, err
		}
		eventAttributes := make(
			map[string]interface{}, len(spec.attributes)+1,
		)
		for key, value := range spec.attributes {
			eventAttributes[key] = value
		}
		if e.workerID != "" {
			eventAttributes["worker_id"] = e.workerID
		}
		event := audit.Event{
			ID: eventID, Type: spec.eventType, Timestamp: e.now(),
			TenantID:    execution.Principal.TenantID,
			ActorID:     execution.Principal.ActorID,
			ExecutionID: execution.ID, WorkflowID: execution.WorkflowID,
			WorkflowVersion: execution.WorkflowVersion,
			StepID:          spec.stepID, ToolName: spec.toolName,
			ApprovalID: spec.approvalID, Outcome: spec.outcome,
			Attributes: eventAttributes,
		}
		if value, ok := eventAttributes["tool_call_id"].(string); ok {
			event.ToolCallID = value
		}
		if value, ok := eventAttributes["input_hash"].(string); ok {
			event.InputHash = value
		}
		if value, ok := eventAttributes["output_hash"].(string); ok {
			event.OutputHash = value
		}
		events = append(events, event)
	}
	return events, nil
}

func (e *Executor) appendAuditEvents(
	ctx context.Context,
	events []audit.Event,
) error {
	if e.audit == nil {
		return nil
	}
	for _, event := range events {
		if err := e.audit.Append(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) validateToolOutputSize(
	toolName string,
	output interface{},
) error {
	if !e.production {
		return nil
	}
	size, err := encodedJSONSize(output)
	if err != nil {
		return &ExecutionError{
			Kind: core.ErrorValidation, ToolName: toolName,
			Message: "tool output is not JSON serializable",
		}
	}
	if size > e.limits.MaxToolOutputBytes {
		return &ExecutionError{
			Kind: core.ErrorValidation, ToolName: toolName,
			Message: fmt.Sprintf(
				"tool output exceeds production limit of %d bytes",
				e.limits.MaxToolOutputBytes,
			),
		}
	}
	return nil
}

func (e *Executor) validateCheckpointSize(execution Execution) error {
	if !e.production {
		return nil
	}
	size, err := encodedJSONSize(execution)
	if err != nil {
		return &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "workflow checkpoint is not JSON serializable",
		}
	}
	if size > e.limits.MaxCheckpointBytes {
		return &ExecutionError{
			Kind: core.ErrorValidation,
			Message: fmt.Sprintf(
				"workflow checkpoint exceeds production limit of %d bytes",
				e.limits.MaxCheckpointBytes,
			),
		}
	}
	return nil
}

func encodedJSONSize(value interface{}) (int, error) {
	encoded, err := json.Marshal(value)
	return len(encoded), err
}

func (e *Executor) emit(ctx context.Context, execution Execution, eventType audit.EventType, stepID, toolName, approvalID, outcome string, attributes map[string]interface{}) error {
	events, err := e.buildAuditEvents(execution, []auditEventSpec{{
		eventType: eventType, stepID: stepID, toolName: toolName,
		approvalID: approvalID, outcome: outcome,
		attributes: attributes,
	}})
	if err != nil {
		return err
	}
	return e.appendAuditEvents(ctx, events)
}

func resolveValue(value Value, execution Execution) (interface{}, bool, error) {
	if value.Ref == "" {
		return value.Literal, true, nil
	}
	parts := strings.Split(value.Ref, ".")
	if len(parts) < 2 {
		return nil, false, fmt.Errorf("invalid reference %q", value.Ref)
	}
	var current interface{}
	switch parts[0] {
	case "input":
		current = execution.Input
	case "context":
		current = execution.Context
	case "steps":
		current = execution.State
	default:
		return nil, false, fmt.Errorf("reference %q must start with input, context, or steps", value.Ref)
	}
	for _, part := range parts[1:] {
		switch typed := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, false, nil
			}
		case []interface{}:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false, nil
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func evaluateCondition(condition Condition, execution Execution) (bool, error) {
	left, leftExists, err := resolveValue(condition.Left, execution)
	if err != nil {
		return false, err
	}
	switch condition.Operator {
	case OpExists:
		return leftExists, nil
	case OpNotExists:
		return !leftExists, nil
	}
	if !leftExists {
		return false, nil
	}
	right, rightExists, err := resolveValue(condition.Right, execution)
	if err != nil {
		return false, err
	}
	if !rightExists {
		return false, nil
	}
	switch condition.Operator {
	case OpEqual:
		return reflect.DeepEqual(left, right), nil
	case OpNotEqual:
		return !reflect.DeepEqual(left, right), nil
	case OpGreater, OpGreaterEqual, OpLess, OpLessEqual:
		leftNumber, leftOK := number(left)
		rightNumber, rightOK := number(right)
		if !leftOK || !rightOK {
			return false, fmt.Errorf("operator %s requires numeric values", condition.Operator)
		}
		switch condition.Operator {
		case OpGreater:
			return leftNumber > rightNumber, nil
		case OpGreaterEqual:
			return leftNumber >= rightNumber, nil
		case OpLess:
			return leftNumber < rightNumber, nil
		default:
			return leftNumber <= rightNumber, nil
		}
	default:
		return false, fmt.Errorf("unsupported condition operator %q", condition.Operator)
	}
}

func number(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, !math.IsNaN(typed)
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return map[string]interface{}{}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		out := make(map[string]interface{}, len(input))
		for key, value := range input {
			out[key] = value
		}
		return out
	}
	var out map[string]interface{}
	if json.Unmarshal(raw, &out) != nil {
		return map[string]interface{}{}
	}
	return out
}

func hashValue(value interface{}) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
