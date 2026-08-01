// Package automation composes observation, learning, review, publication, and
// deterministic execution into one safe application-facing lifecycle. The
// underlying domain packages remain independently usable.
package automation

import (
	"context"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type Options struct {
	Recorder       *observation.Recorder
	Demonstrations observation.Store
	Compiler       *learning.Compiler
	Evaluator      *evaluation.Runner
	Publisher      *evaluation.Publisher
	Reviews        workflow.ReviewStore
	Workflows      workflow.Store
	Executor       *workflow.Executor
	Resolver       *workflow.Resolver
	Routes         workflow.RouteStore
	Executions     workflow.ExecutionStore
	Feedback       workflow.FeedbackStore
	Approvals      policy.ApprovalLifecycleStore
	Audit          audit.Sink
	Now            func() time.Time
}

type Lifecycle struct {
	recorder       *observation.Recorder
	demonstrations observation.Store
	compiler       *learning.Compiler
	evaluator      *evaluation.Runner
	publisher      *evaluation.Publisher
	reviews        workflow.ReviewStore
	workflows      workflow.Store
	executor       *workflow.Executor
	resolver       *workflow.Resolver
	routes         workflow.RouteStore
	executions     workflow.ExecutionStore
	feedback       workflow.FeedbackStore
	approvals      policy.ApprovalLifecycleStore
	audit          audit.Sink
	now            func() time.Time
}

func New(options Options) (*Lifecycle, error) {
	if options.Recorder == nil || options.Demonstrations == nil ||
		options.Compiler == nil || options.Evaluator == nil ||
		options.Publisher == nil || options.Reviews == nil ||
		options.Workflows == nil || options.Executor == nil {
		return nil, core.NewConfigError(
			"automation lifecycle requires recorder, demonstration, compiler, evaluator, publisher, review, workflow, and executor dependencies",
		)
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Lifecycle{
		recorder: options.Recorder, demonstrations: options.Demonstrations,
		compiler: options.Compiler, evaluator: options.Evaluator,
		publisher: options.Publisher, reviews: options.Reviews,
		workflows: options.Workflows, executor: options.Executor,
		resolver: options.Resolver, routes: options.Routes,
		executions: options.Executions,
		feedback:   options.Feedback,
		approvals:  options.Approvals,
		audit:      options.Audit, now: options.Now,
	}, nil
}

// SaveRoute creates or updates an exact task route after verifying that its
// target workflow currently has a published version.
func (l *Lifecycle) SaveRoute(
	ctx context.Context,
	route workflow.Route,
	principal core.Principal,
) (workflow.Route, error) {
	if !authenticatedIdentity(ctx, principal) {
		return workflow.Route{}, core.NewPermissionError(
			"workflow route change requires the authenticated actor identity",
		)
	}
	if l.routes == nil {
		return workflow.Route{}, core.NewConfigError(
			"automation lifecycle workflow route store is not configured",
		)
	}
	version, exists, err := l.workflows.Published(ctx, route.WorkflowID)
	if err != nil {
		return workflow.Route{}, err
	}
	if !exists {
		return workflow.Route{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow route target has no published version",
		}
	}
	previousRevision := route.Revision
	saved, err := l.routes.Save(ctx, route)
	if err != nil {
		return workflow.Route{}, err
	}
	if l.audit != nil {
		outcome := "created"
		if previousRevision > 0 {
			outcome = "updated"
		}
		eventID, err := id.New()
		if err != nil {
			return saved, err
		}
		if err := l.audit.Append(ctx, audit.Event{
			ID: eventID, Type: audit.EventRouteChanged, Timestamp: l.now(),
			TenantID: principal.TenantID, ActorID: principal.ActorID,
			WorkflowID: saved.WorkflowID, WorkflowVersion: version.Version,
			Outcome: outcome, Attributes: map[string]interface{}{
				"task_type": saved.TaskType, "route_revision": saved.Revision,
			},
		}); err != nil {
			return saved, &core.SkawldError{
				Kind:    core.ErrorWorkflow,
				Message: "workflow route was stored but its audit event failed",
				Cause:   err,
			}
		}
	}
	return saved, nil
}

func (l *Lifecycle) DeleteRoute(
	ctx context.Context,
	taskType string,
	revision int64,
	principal core.Principal,
) error {
	if !authenticatedIdentity(ctx, principal) {
		return core.NewPermissionError(
			"workflow route deletion requires the authenticated actor identity",
		)
	}
	if l.routes == nil {
		return core.NewConfigError(
			"automation lifecycle workflow route store is not configured",
		)
	}
	route, exists, err := l.routes.Get(ctx, taskType)
	if err != nil {
		return err
	}
	if !exists {
		return &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "workflow route not found",
		}
	}
	if err := l.routes.Delete(ctx, taskType, revision); err != nil {
		return err
	}
	if l.audit != nil {
		eventID, err := id.New()
		if err != nil {
			return err
		}
		if err := l.audit.Append(ctx, audit.Event{
			ID: eventID, Type: audit.EventRouteChanged, Timestamp: l.now(),
			TenantID: principal.TenantID, ActorID: principal.ActorID,
			WorkflowID: route.WorkflowID, Outcome: "deleted",
			Attributes: map[string]interface{}{
				"task_type": route.TaskType, "route_revision": revision,
			},
		}); err != nil {
			return &core.SkawldError{
				Kind:    core.ErrorWorkflow,
				Message: "workflow route was deleted but its audit event failed",
				Cause:   err,
			}
		}
	}
	return nil
}

// RecordFeedback attaches a bounded semantic label to a terminal execution.
// It does not automatically retrain, modify, or republish a workflow.
func (l *Lifecycle) RecordFeedback(
	ctx context.Context,
	executionID string,
	request workflow.FeedbackRequest,
	principal core.Principal,
) (workflow.ExecutionFeedback, error) {
	if !authenticatedIdentity(ctx, principal) {
		return workflow.ExecutionFeedback{}, core.NewPermissionError(
			"workflow feedback requires the authenticated actor identity",
		)
	}
	if l.executions == nil || l.feedback == nil {
		return workflow.ExecutionFeedback{}, core.NewConfigError(
			"automation lifecycle execution and feedback stores are not configured",
		)
	}
	execution, exists, err := l.executions.Get(ctx, executionID)
	if err != nil {
		return workflow.ExecutionFeedback{}, err
	}
	if !exists {
		return workflow.ExecutionFeedback{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "workflow execution not found",
		}
	}
	feedback, err := workflow.NewExecutionFeedback(
		execution, request, principal, l.now(),
	)
	if err != nil {
		return workflow.ExecutionFeedback{}, err
	}
	if err := l.feedback.Save(ctx, feedback); err != nil {
		return workflow.ExecutionFeedback{}, err
	}
	if l.audit != nil {
		eventID, err := id.New()
		if err != nil {
			return feedback, err
		}
		if err := l.audit.Append(ctx, audit.Event{
			ID: eventID, Type: audit.EventFeedbackRecorded, Timestamp: l.now(),
			TenantID: principal.TenantID, ActorID: principal.ActorID,
			ExecutionID: execution.ID, WorkflowID: execution.WorkflowID,
			WorkflowVersion: execution.WorkflowVersion, StepID: feedback.StepID,
			Outcome: string(feedback.Disposition),
			Attributes: map[string]interface{}{
				"feedback_id": feedback.ID, "reason_code": feedback.ReasonCode,
			},
		}); err != nil {
			return feedback, &core.SkawldError{
				Kind:    core.ErrorWorkflow,
				Message: "workflow feedback was stored but its audit event failed",
				Cause:   err,
			}
		}
	}
	return feedback, nil
}

// ExecuteTask deterministically resolves an exact task type or workflow ID and
// executes the published version. It never asks an LLM to select a workflow.
func (l *Lifecycle) ExecuteTask(
	ctx context.Context,
	request workflow.ResolutionRequest,
	input map[string]interface{},
	workflowContext map[string]interface{},
	principal core.Principal,
) (workflow.Execution, error) {
	if !authenticatedIdentity(ctx, principal) {
		return workflow.Execution{}, core.NewPermissionError(
			"workflow execution requires the authenticated actor identity",
		)
	}
	if l.resolver == nil {
		return workflow.Execution{}, core.NewConfigError(
			"automation lifecycle workflow resolver is not configured",
		)
	}
	version, err := l.resolver.Resolve(ctx, request)
	if err != nil {
		return workflow.Execution{}, err
	}
	return l.executor.Execute(ctx, version, input, workflowContext, principal)
}

func (l *Lifecycle) StartDemonstration(
	ctx context.Context,
	workflowKey string,
	principal core.Principal,
	initialContext map[string]interface{},
) (observation.Demonstration, error) {
	if !authenticatedIdentity(ctx, principal) {
		return observation.Demonstration{}, core.NewPermissionError(
			"demonstration start requires the authenticated actor identity",
		)
	}
	return l.recorder.Start(ctx, workflowKey, principal, initialContext)
}

func (l *Lifecycle) Capture(
	ctx context.Context,
	demonstrationID string,
	event observation.Event,
) (observation.Event, error) {
	return l.recorder.Capture(ctx, demonstrationID, event)
}

func (l *Lifecycle) CompleteDemonstration(
	ctx context.Context,
	demonstrationID string,
	result map[string]interface{},
) (observation.Demonstration, error) {
	return l.recorder.Complete(ctx, demonstrationID, result)
}

func (l *Lifecycle) Learn(
	ctx context.Context,
	workflowKey string,
	workflowID string,
	workflowName string,
	options learning.MultiDemoOptions,
) (learning.CompilationResult, error) {
	demonstrations, err := l.demonstrations.List(ctx, workflowKey)
	if err != nil {
		return learning.CompilationResult{}, err
	}
	completed := make([]observation.Demonstration, 0, len(demonstrations))
	for _, demonstration := range demonstrations {
		if demonstration.Status == observation.DemonstrationCompleted {
			completed = append(completed, demonstration)
		}
	}
	return l.compiler.CompileMultiple(
		ctx, workflowID, workflowName, completed, options,
	)
}

func (l *Lifecycle) Evaluate(
	ctx context.Context,
	workflowID string,
	version int,
	suite evaluation.Suite,
) (evaluation.Report, error) {
	candidate, exists, err := l.workflows.Get(ctx, workflowID, version)
	if err != nil {
		return evaluation.Report{}, err
	}
	if !exists {
		return evaluation.Report{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "workflow candidate not found",
		}
	}
	return l.evaluator.Run(ctx, suite, candidate)
}

func (l *Lifecycle) Review(
	ctx context.Context,
	workflowID string,
	version int,
	decision workflow.ReviewDecision,
	reason string,
	principal core.Principal,
) (workflow.Review, error) {
	authenticated, ok := core.PrincipalFromContext(ctx)
	if !ok || authenticated.TenantID != principal.TenantID ||
		authenticated.ActorID != principal.ActorID {
		return workflow.Review{}, core.NewPermissionError(
			"workflow review requires the authenticated reviewer identity",
		)
	}
	candidate, exists, err := l.workflows.Get(ctx, workflowID, version)
	if err != nil {
		return workflow.Review{}, err
	}
	if !exists {
		return workflow.Review{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "workflow candidate not found",
		}
	}
	review, err := workflow.NewReview(candidate, decision, principal, reason, l.now())
	if err != nil {
		return workflow.Review{}, err
	}
	if err := l.reviews.Save(ctx, review); err != nil {
		return workflow.Review{}, err
	}
	if l.audit != nil {
		eventID, err := id.New()
		if err != nil {
			return review, err
		}
		if err := l.audit.Append(ctx, audit.Event{
			ID: eventID, Type: audit.EventWorkflowReviewed, Timestamp: l.now(),
			TenantID: principal.TenantID, ActorID: principal.ActorID,
			WorkflowID: workflowID, WorkflowVersion: version,
			Outcome: string(decision), Attributes: map[string]interface{}{
				"review_id": review.ID, "candidate_digest": review.CandidateDigest,
			},
		}); err != nil {
			return review, &core.SkawldError{
				Kind:    core.ErrorWorkflow,
				Message: "workflow review was stored but its audit event failed",
				Cause:   err,
			}
		}
	}
	return review, nil
}

func (l *Lifecycle) Publish(
	ctx context.Context,
	workflowID string,
	version int,
	principal core.Principal,
) (workflow.Version, error) {
	return l.publisher.Publish(ctx, workflowID, version, principal)
}

func (l *Lifecycle) ExecutePublished(
	ctx context.Context,
	workflowID string,
	input map[string]interface{},
	workflowContext map[string]interface{},
	principal core.Principal,
) (workflow.Execution, error) {
	if !authenticatedIdentity(ctx, principal) {
		return workflow.Execution{}, core.NewPermissionError(
			"workflow execution requires the authenticated actor identity",
		)
	}
	version, exists, err := l.workflows.Published(ctx, workflowID)
	if err != nil {
		return workflow.Execution{}, err
	}
	if !exists {
		return workflow.Execution{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "published workflow not found",
		}
	}
	return l.executor.Execute(ctx, version, input, workflowContext, principal)
}

func authenticatedIdentity(ctx context.Context, principal core.Principal) bool {
	authenticated, ok := core.PrincipalFromContext(ctx)
	return ok && authenticated.TenantID != "" && authenticated.ActorID != "" &&
		authenticated.TenantID == principal.TenantID &&
		authenticated.ActorID == principal.ActorID
}
