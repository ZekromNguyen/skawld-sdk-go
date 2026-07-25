package automation

import (
	"context"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func TestLifecycleDemonstrateLearnEvaluateReviewPublishExecute(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demonstrations := observation.NewMemoryStore()
	recorder, err := observation.NewRecorder(demonstrations)
	if err != nil {
		t.Fatal(err)
	}
	workflows := workflow.NewMemoryStore()
	reviews := workflow.NewMemoryReviewStore()
	reports := evaluation.NewMemoryStore()
	audits := &audit.MemoryStore{}
	executions := workflow.NewMemoryExecutionStore()
	feedbackStore := workflow.NewMemoryFeedbackStore()
	routes := workflow.NewMemoryRouteStore()
	resolver, err := workflow.NewResolver(workflow.ResolverOptions{
		Store: workflows, RouteStore: routes,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiler := &learning.Compiler{
		Extractor: lifecycleExtractor{}, Tools: lifecycleTools{}, Store: workflows,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"invoice": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"valid": map[string]interface{}{"type": "boolean"},
					},
				},
			},
		},
	}
	suite := evaluation.Suite{
		Name: "invoice-release",
		Scenarios: []evaluation.Scenario{{
			ID: "valid-invoice",
			Input: map[string]interface{}{
				"invoice": map[string]interface{}{"valid": true},
			},
			Expected: evaluation.ExpectedOutcome{Status: workflow.ExecutionCompleted},
		}},
		Gates: []evaluation.Gate{{
			Metric:   evaluation.MetricTaskSuccessRate,
			Operator: evaluation.GateAtLeast, Value: 1,
		}},
	}
	runner := evaluation.NewRunner(evaluation.RunnerOptions{Store: reports})
	publisher, err := evaluation.NewPublisher(evaluation.PublisherOptions{
		Workflows: workflows, Reports: reports, Reviews: reviews,
		RequiredSuite: suite.Name, Audit: audits,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := workflow.NewExecutor(workflow.ExecutorOptions{
		Tools: lifecycleTools{}, Audit: audits, Executions: executions,
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := New(Options{
		Recorder: recorder, Demonstrations: demonstrations, Compiler: compiler,
		Evaluator: runner, Publisher: publisher, Reviews: reviews,
		Workflows: workflows, Executor: executor, Audit: audits,
		Resolver: resolver, Routes: routes,
		Executions: executions, Feedback: feedbackStore,
	})
	if err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 2; index++ {
		demonstration, err := lifecycle.StartDemonstration(
			ctx, "invoice", principal, map[string]interface{}{"source": "test"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lifecycle.Capture(ctx, demonstration.ID, observation.Event{
			ID: "event-" + string(rune('a'+index)), Source: observation.SourceAPI,
			Trust: observation.TrustApplicationEvent, Application: "erp",
			Action: "validate_invoice",
			Input:  map[string]interface{}{"valid": true},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := lifecycle.CompleteDemonstration(
			ctx, demonstration.ID, map[string]interface{}{"accepted": true},
		); err != nil {
			t.Fatal(err)
		}
	}
	compilation, err := lifecycle.Learn(
		ctx, "invoice", "invoice", "Invoice", learning.MultiDemoOptions{
			MinimumDemonstrations: 2, MinimumSequenceConsistency: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := lifecycle.Evaluate(
		ctx, compilation.Candidate.Workflow.ID, compilation.Candidate.Version, suite,
	)
	if err != nil || !report.Gates.Passed {
		t.Fatalf("evaluation: report=%+v err=%v", report, err)
	}
	if _, err := lifecycle.Publish(
		ctx, compilation.Candidate.Workflow.ID, compilation.Candidate.Version, principal,
	); err == nil {
		t.Fatal("expected publication without human review to fail")
	}
	if _, err := lifecycle.Review(
		ctx, compilation.Candidate.Workflow.ID, compilation.Candidate.Version,
		workflow.ReviewApproved, "verified mapping", principal,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.Publish(
		ctx, compilation.Candidate.Workflow.ID, compilation.Candidate.Version, principal,
	); err != nil {
		t.Fatal(err)
	}
	route, err := lifecycle.SaveRoute(ctx, workflow.Route{
		TaskType: "invoice.validate", WorkflowID: "invoice",
	}, principal)
	if err != nil || route.Revision != 1 {
		t.Fatalf("save route: route=%+v err=%v", route, err)
	}
	execution, err := lifecycle.ExecuteTask(
		ctx, workflow.ResolutionRequest{TaskType: "invoice.validate"},
		map[string]interface{}{"invoice": map[string]interface{}{"valid": true}},
		nil, principal,
	)
	if err != nil || execution.Status != workflow.ExecutionCompleted {
		t.Fatalf("execution: %+v err=%v", execution, err)
	}
	feedback, err := lifecycle.RecordFeedback(
		ctx, execution.ID,
		workflow.FeedbackRequest{
			Disposition: workflow.FeedbackAccepted,
			ReasonCode:  "validated",
			Comment:     "The deterministic validation was correct.",
		},
		principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if feedback.ExecutionID != execution.ID {
		t.Fatalf("feedback does not reference execution: %+v", feedback)
	}
	if _, err := lifecycle.RecordFeedback(
		ctx, execution.ID,
		workflow.FeedbackRequest{
			Disposition: workflow.FeedbackCorrection,
			ReasonCode:  "missing.review", CorrectedAction: "review_invoice",
		},
		principal,
	); err != nil {
		t.Fatal(err)
	}
	newDemonstration, err := lifecycle.StartDemonstration(
		ctx, "invoice", principal, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.Capture(
		ctx, newDemonstration.ID, observation.Event{
			ID: "event-new", Source: observation.SourceAPI,
			Trust: observation.TrustApplicationEvent, Application: "erp",
			Action: "validate_invoice",
			Input:  map[string]interface{}{"valid": true},
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CompleteDemonstration(
		ctx, newDemonstration.ID, map[string]interface{}{"accepted": true},
	); err != nil {
		t.Fatal(err)
	}
	proposal, err := lifecycle.ProposeImprovement(
		ctx, ImprovementRequest{
			WorkflowKey: "invoice", WorkflowID: "invoice", BaseVersion: 1,
			MinimumFeedback: 1,
			Learning: learning.MultiDemoOptions{
				MinimumDemonstrations:      2,
				MinimumSequenceConsistency: 1,
			},
		}, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Compilation.Candidate.Version != 2 ||
		proposal.Compilation.Candidate.Status != workflow.VersionCandidate ||
		len(proposal.NewDemonstrationIDs) != 1 {
		t.Fatalf("unexpected improvement proposal: %+v", proposal)
	}
	events, err := audits.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var reviewed, published, routeChanged, feedbackRecorded bool
	for _, event := range events {
		reviewed = reviewed || event.Type == audit.EventWorkflowReviewed
		published = published || event.Type == audit.EventWorkflowPublished
		routeChanged = routeChanged || event.Type == audit.EventRouteChanged
		feedbackRecorded = feedbackRecorded || event.Type == audit.EventFeedbackRecorded
	}
	if !reviewed || !published || !routeChanged || !feedbackRecorded {
		t.Fatalf("lifecycle audit events missing: %+v", events)
	}
}

type lifecycleExtractor struct{}

func (lifecycleExtractor) Extract(
	_ context.Context,
	request learning.ExtractionRequest,
) (workflow.Version, error) {
	evidence := make([]workflow.EvidenceRef, 0, len(request.Demonstrations))
	for _, demonstration := range request.Demonstrations {
		evidence = append(evidence, workflow.EvidenceRef{
			DemonstrationID: demonstration.ID,
			EventIDs:        []string{demonstration.Trace.Events[0].ID},
		})
	}
	return workflow.Version{
		InputSchema: request.InputSchema,
		Steps: []workflow.Step{{
			ID: "validate", Kind: workflow.StepValidation, Evidence: evidence,
			Validation: &workflow.Validation{Condition: workflow.Condition{
				Left:     workflow.Value{Ref: "input.invoice.valid"},
				Operator: workflow.OpEqual, Right: workflow.Value{Literal: true},
			}},
		}},
	}, nil
}

type lifecycleTools struct{}

func (lifecycleTools) Describe(
	context.Context,
	string,
) (core.ToolDescriptor, bool, error) {
	return core.ToolDescriptor{}, false, nil
}

func (lifecycleTools) Execute(
	context.Context,
	string,
	map[string]interface{},
	string,
) (workflow.ToolResult, error) {
	return workflow.ToolResult{}, nil
}
