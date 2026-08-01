package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// VersionResolver resolves the immutable version attached to a durable
// execution. Store satisfies this interface.
type VersionResolver interface {
	Get(context.Context, string, int) (Version, bool, error)
}

type CoordinatorOptions struct {
	Executor   *Executor
	Executions ExecutionStore
	Versions   VersionResolver
	MaxBatch   int
	// WorkerRole authorizes a service principal to adopt the immutable
	// initiating identity stored in a checkpoint while resuming it. It defaults
	// to workflow.worker.
	WorkerRole string
}

type Coordinator struct {
	executor   *Executor
	claimer    ReadyExecutionClaimer
	versions   VersionResolver
	maxBatch   int
	workerRole string
	healthMu   sync.RWMutex
	health     CoordinatorHealth
}

type CoordinationReport struct {
	Scanned          int
	Resumed          int
	AwaitingApproval int
	RecoveryRequired int
	Failed           int
}

type CoordinationObserver func(CoordinationReport, error)

// CoordinatorHealth exposes content-free liveness/readiness state without
// workflow inputs, errors, or tenant data.
type CoordinatorHealth struct {
	WorkerID            string
	Ready               bool
	LastAttemptAt       time.Time
	LastSuccessAt       time.Time
	ConsecutiveFailures int
	LastReport          CoordinationReport
}

func (h CoordinatorHealth) Healthy(
	now time.Time,
	maxStaleness time.Duration,
) bool {
	if !h.Ready || h.LastAttemptAt.IsZero() ||
		maxStaleness <= 0 || now.Before(h.LastAttemptAt) {
		return false
	}
	return now.Sub(h.LastAttemptAt) <= maxStaleness
}

func NewCoordinator(options CoordinatorOptions) (*Coordinator, error) {
	if options.Executor == nil || options.Executions == nil ||
		options.Versions == nil {
		return nil, core.NewConfigError(
			"workflow coordinator requires executor, executions, and versions",
		)
	}
	if options.Executor.workerID == "" ||
		!options.Executor.requireLease {
		return nil, core.NewConfigError(
			"workflow coordinator requires a lease-enforcing executor",
		)
	}
	claimer, ok := options.Executions.(ReadyExecutionClaimer)
	if !ok {
		return nil, core.NewConfigError(
			"workflow coordinator requires atomic ready-execution claiming",
		)
	}
	if options.MaxBatch == 0 {
		options.MaxBatch = 100
	}
	if options.MaxBatch < 1 || options.MaxBatch > 1000 {
		return nil, core.NewConfigError(
			"workflow coordinator batch size must be between 1 and 1000",
		)
	}
	if strings.TrimSpace(options.WorkerRole) == "" {
		options.WorkerRole = "workflow.worker"
	}
	return &Coordinator{
		executor: options.Executor, claimer: claimer,
		versions: options.Versions, maxBatch: options.MaxBatch,
		workerRole: strings.TrimSpace(options.WorkerRole),
	}, nil
}

// RunOnce scans bounded non-terminal work for the authenticated tenant. A
// service role is required because the coordinator resumes under the trusted
// initiating identity stored in each checkpoint.
func (c *Coordinator) RunOnce(
	ctx context.Context,
) (report CoordinationReport, runErr error) {
	attemptedAt := c.executor.now()
	defer func() {
		c.recordHealth(attemptedAt, report, runErr)
	}()
	worker, ok := core.PrincipalFromContext(ctx)
	if !ok || !worker.Authenticated() ||
		!containsRole(worker.Roles, c.workerRole) {
		return CoordinationReport{}, core.NewPermissionError(
			"workflow coordination requires an authenticated worker role",
		)
	}
	claims, err := c.claimer.ClaimReadyExecutions(
		ctx, ReadyExecutionClaimRequest{
			Statuses: []ExecutionStatus{
				ExecutionRunning, ExecutionAwaitingApproval,
			},
			Owner: c.executor.workerID, Duration: c.executor.leaseDuration,
			Limit: c.maxBatch,
		},
	)
	if err != nil {
		return report, err
	}
	var failures []error
	for _, claim := range claims {
		execution := claim.Execution
		report.Scanned++
		version, exists, err := c.versions.Get(
			ctx, execution.WorkflowID, execution.WorkflowVersion,
		)
		if err != nil {
			_ = c.claimer.ReleaseExecution(
				WithExecutionClaim(ctx, claim), claim,
			)
			report.Failed++
			failures = append(failures, err)
			continue
		}
		if !exists {
			_ = c.claimer.ReleaseExecution(
				WithExecutionClaim(ctx, claim), claim,
			)
			report.Failed++
			failures = append(failures, fmt.Errorf(
				"workflow version %s/%d was not found",
				execution.WorkflowID, execution.WorkflowVersion,
			))
			continue
		}
		executionCtx := core.WithPrincipal(ctx, execution.Principal)
		executionCtx = WithExecutionClaim(executionCtx, claim)
		resumed, err := c.executor.ResumeStored(
			executionCtx, version, execution.ID,
		)
		if err != nil {
			_ = c.claimer.ReleaseExecution(
				WithExecutionClaim(ctx, claim), claim,
			)
			var sdkErr *core.SkawldError
			if errors.As(err, &sdkErr) &&
				sdkErr.Kind == core.ErrorConflict {
				continue
			}
			report.Failed++
			failures = append(failures, fmt.Errorf(
				"coordinate execution %q: %w", execution.ID, err,
			))
			continue
		}
		switch resumed.Status {
		case ExecutionAwaitingApproval:
			report.AwaitingApproval++
		case ExecutionRecoveryRequired:
			report.RecoveryRequired++
		default:
			report.Resumed++
		}
	}
	return report, errors.Join(failures...)
}

func (c *Coordinator) Health() CoordinatorHealth {
	if c == nil {
		return CoordinatorHealth{}
	}
	c.healthMu.RLock()
	defer c.healthMu.RUnlock()
	return c.health
}

func (c *Coordinator) recordHealth(
	attemptedAt time.Time,
	report CoordinationReport,
	err error,
) {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	c.health.WorkerID = c.executor.workerID
	c.health.LastAttemptAt = attemptedAt
	c.health.LastReport = report
	if err != nil {
		c.health.Ready = false
		c.health.ConsecutiveFailures++
		return
	}
	c.health.Ready = true
	c.health.LastSuccessAt = c.executor.now()
	c.health.ConsecutiveFailures = 0
}

// Run polls until cancellation. Without an observer, a coordination error
// stops the worker; with an observer, errors are reported and the next bounded
// poll continues. A poll never overlaps the previous poll.
func (c *Coordinator) Run(
	ctx context.Context,
	interval time.Duration,
	observe CoordinationObserver,
) error {
	if interval < 100*time.Millisecond || interval > 5*time.Minute {
		return core.NewConfigError(
			"workflow coordinator interval must be between 100ms and five minutes",
		)
	}
	run := func() error {
		report, err := c.RunOnce(ctx)
		if observe != nil {
			observe(report, err)
			return nil
		}
		return err
	}
	if err := run(); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := run(); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ResumeStored loads the authoritative checkpoint instead of trusting a
// caller-supplied serialized execution.
func (e *Executor) ResumeStored(
	ctx context.Context,
	version Version,
	executionID string,
) (Execution, error) {
	if e.executions == nil {
		return Execution{}, core.NewConfigError(
			"stored workflow resume requires an execution store",
		)
	}
	checkpoint, exists, err := e.executions.Get(ctx, executionID)
	if err != nil {
		return Execution{}, err
	}
	if !exists {
		return Execution{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution not found",
		}
	}
	switch checkpoint.Status {
	case ExecutionAwaitingApproval:
		return e.Resume(ctx, version, checkpoint)
	case ExecutionRunning:
		return e.resumeInterrupted(ctx, version, checkpoint)
	case ExecutionRecoveryRequired:
		return checkpoint, nil
	default:
		return checkpoint, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "terminal workflow execution cannot be resumed",
		}
	}
}

func (e *Executor) resumeInterrupted(
	ctx context.Context,
	version Version,
	checkpoint Execution,
) (Execution, error) {
	if _, err := requireExecutionIdentity(ctx, checkpoint.Principal); err != nil {
		return checkpoint, &ExecutionError{
			Kind: core.ErrorPermissionDenied, Message: err.Error(),
		}
	}
	if checkpoint.WorkflowID != version.Workflow.ID ||
		checkpoint.WorkflowVersion != version.Version {
		return checkpoint, &ExecutionError{
			Kind:    core.ErrorValidation,
			Message: "checkpoint workflow version mismatch",
		}
	}
	runCtx, claimed, release, err := e.acquireExecution(ctx, checkpoint)
	if err != nil {
		return checkpoint, err
	}
	defer release()
	ctx = runCtx
	checkpoint = claimed
	if err := e.validateToolCatalog(ctx, version); err != nil {
		return checkpoint, err
	}
	if err := e.validateReferences(ctx, version); err != nil {
		return checkpoint, err
	}
	if checkpoint.NextStep < len(version.Steps) {
		step := version.Steps[checkpoint.NextStep]
		run := &checkpoint.Steps[checkpoint.NextStep]
		if run.Status == StepRunning && step.Kind == StepTool {
			descriptor, exists, err := e.tools.Describe(
				ctx, step.Tool.Name,
			)
			if err != nil {
				return checkpoint, err
			}
			if !exists {
				return checkpoint, &ExecutionError{
					Kind: core.ErrorNotFound, StepID: step.ID,
					ToolName: step.Tool.Name,
					Message:  "interrupted workflow tool is not registered",
				}
			}
			if descriptor.SideEffect != core.SideEffectNone {
				reason := "worker stopped while a side-effecting tool call was in flight"
				run.Status = StepRecoveryRequired
				run.Error = &ExecutionError{
					Kind: core.ErrorWorkflow, StepID: step.ID,
					ToolName: step.Tool.Name, Message: reason,
				}
				checkpoint.Status = ExecutionRecoveryRequired
				checkpoint.Error = run.Error
				if err := e.checkpointWithEvents(
					context.WithoutCancel(ctx), &checkpoint,
					auditEventSpec{
						eventType: audit.EventRecoveryRequired,
						stepID:    step.ID, toolName: step.Tool.Name,
						outcome: "recovery_required",
						attributes: map[string]interface{}{
							"reason":    reason,
							"worker_id": e.workerID,
						},
					},
				); err != nil {
					return checkpoint, err
				}
				return checkpoint, nil
			}
		}
	}
	return e.run(ctx, version, checkpoint)
}

func containsRole(roles []string, required string) bool {
	for _, role := range roles {
		if strings.TrimSpace(role) == required {
			return true
		}
	}
	return false
}
