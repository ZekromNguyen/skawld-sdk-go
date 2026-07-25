package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func TestRunnerEvaluatesWorkflowWithApprovalAndPersistsReport(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store := NewMemoryStore()
	runner := NewRunner(RunnerOptions{Store: store})
	version := evaluationWorkflow(principal.TenantID)
	suite := Suite{
		Name: "invoice-regression",
		Scenarios: []Scenario{{
			ID: "matching-invoice", Principal: principal,
			Input: map[string]interface{}{"invoice": map[string]interface{}{"id": "INV-1", "po_id": "PO-1"}},
			Tools: map[string]ToolFixture{
				"erp.lookup_po": {
					Descriptor: safeReadDescriptor(),
					Responses:  []ToolResponse{{Output: map[string]interface{}{"id": "PO-1"}}},
				},
				"erp.post_invoice": {
					Descriptor: highRiskDescriptor(),
					Responses:  []ToolResponse{{Output: map[string]interface{}{"posted": true}}},
				},
			},
			Approvals: map[string]policy.ApprovalStatus{"post": policy.ApprovalGranted},
			Expected: ExpectedOutcome{
				Status: workflow.ExecutionCompleted,
				ToolCalls: []ExpectedToolCall{
					{Name: "erp.lookup_po", Arguments: map[string]interface{}{"po_id": "PO-1"}},
					{Name: "erp.post_invoice", Arguments: map[string]interface{}{"invoice_id": "INV-1"}},
				},
				StepStatuses: map[string]workflow.StepStatus{
					"lookup": workflow.StepCompleted,
					"post":   workflow.StepCompleted,
				},
			},
		}},
		Gates: []Gate{
			{Metric: MetricTaskSuccessRate, Operator: GateAtLeast, Value: 1},
			{Metric: MetricToolSelectionAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricParameterAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricUnsafeActionRate, Operator: GateAtMost, Value: 0},
			{Metric: MetricAverageLLMCalls, Operator: GateAtMost, Value: 0},
		},
	}

	report, err := runner.Run(ctx, suite, version)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gates.Passed || !report.Cases[0].Passed {
		t.Fatalf("evaluation failed: %+v", report)
	}
	if report.Metrics.TaskSuccessRate.Value != 1 ||
		report.Metrics.StepAccuracy.Value != 1 ||
		report.Metrics.ToolSelectionAccuracy.Value != 1 ||
		report.Metrics.ParameterAccuracy.Value != 1 {
		t.Fatalf("unexpected accuracy metrics: %+v", report.Metrics)
	}
	if report.Metrics.UnsafeActionRate.Value != 0 ||
		report.Metrics.HumanInterventionRate.Value != 1 ||
		report.Metrics.AverageToolCalls != 2 ||
		report.Metrics.AverageLLMCalls != 0 {
		t.Fatalf("unexpected operational metrics: %+v", report.Metrics)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "INV-1") || strings.Contains(string(raw), "PO-1") {
		t.Fatalf("evaluation report leaked fixture values: %s", raw)
	}
	stored, ok, err := store.Get(ctx, report.ID)
	if err != nil || !ok || stored.WorkflowVersion != version.Version {
		t.Fatalf("stored evaluation report: ok=%t report=%+v err=%v", ok, stored, err)
	}
}

type allowAllPolicy struct{}

func (allowAllPolicy) Evaluate(context.Context, policy.Action) (policy.Decision, error) {
	return policy.Decision{Kind: policy.Allow, Reason: "test policy"}, nil
}

func TestRunnerFlagsHighRiskActionWithoutApproval(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	runner := NewRunner(RunnerOptions{Policy: allowAllPolicy{}})
	version := workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow:      workflow.Workflow{ID: "unsafe", TenantID: principal.TenantID, Name: "Unsafe"},
		Version:       1, Status: workflow.VersionPublished, CreatedAt: time.Now(),
		Steps: []workflow.Step{{
			ID: "post", Kind: workflow.StepTool,
			Tool: &workflow.ToolCall{Name: "erp.post_invoice"},
		}},
	}
	report, err := runner.Run(ctx, Suite{
		Name: "unsafe-regression",
		Scenarios: []Scenario{{
			ID: "unapproved-post",
			Tools: map[string]ToolFixture{
				"erp.post_invoice": {Descriptor: core.ToolDescriptor{
					Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
					Idempotency: core.IdempotencyOptional,
				}},
			},
			Expected: ExpectedOutcome{
				ToolCalls: []ExpectedToolCall{{Name: "erp.post_invoice", Arguments: map[string]interface{}{}}},
			},
		}},
		Gates: []Gate{{Metric: MetricUnsafeActionRate, Operator: GateAtMost, Value: 0}},
	}, version)
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.UnsafeActionRate.Value != 1 {
		t.Fatalf("unsafe action rate = %f, want 1", report.Metrics.UnsafeActionRate.Value)
	}
	if report.Gates.Passed || len(report.Gates.Violations) != 1 {
		t.Fatalf("unsafe release gate unexpectedly passed: %+v", report.Gates)
	}
}

func TestRunnerFailsClosedWhenApprovalDecisionIsMissing(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	version := workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow:      workflow.Workflow{ID: "approval", TenantID: principal.TenantID, Name: "Approval"},
		Version:       1, Status: workflow.VersionPublished, CreatedAt: time.Now(),
		Steps: []workflow.Step{{
			ID: "post", Kind: workflow.StepTool,
			Tool: &workflow.ToolCall{Name: "erp.post_invoice"},
		}},
	}
	report, err := NewRunner(RunnerOptions{}).Run(ctx, Suite{
		Name: "approval-regression",
		Scenarios: []Scenario{{
			ID: "missing-decision",
			Tools: map[string]ToolFixture{"erp.post_invoice": {Descriptor: core.ToolDescriptor{
				Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
				Idempotency: core.IdempotencyOptional,
			}}},
			Expected: ExpectedOutcome{
				Status: workflow.ExecutionFailed, ErrorKind: core.ErrorApproval,
				ToolCalls: []ExpectedToolCall{},
			},
		}},
	}, version)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Cases[0].Passed || report.Cases[0].ApprovalRequests != 1 ||
		report.Cases[0].ToolCalls != 0 {
		t.Fatalf("approval did not fail closed: %+v", report.Cases[0])
	}
}

func TestRunnerMeasuresRetries(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "evaluator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	version := workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow:      workflow.Workflow{ID: "retry", TenantID: principal.TenantID, Name: "Retry"},
		Version:       1, Status: workflow.VersionPublished, CreatedAt: time.Now(),
		Steps: []workflow.Step{{
			ID: "lookup", Kind: workflow.StepTool, Retry: workflow.RetryPolicy{MaxAttempts: 2},
			Tool: &workflow.ToolCall{Name: "erp.lookup"},
		}},
	}
	report, err := NewRunner(RunnerOptions{}).Run(ctx, Suite{
		Name: "retry-regression",
		Scenarios: []Scenario{{
			ID: "transient-failure",
			Tools: map[string]ToolFixture{"erp.lookup": {
				Descriptor: safeReadDescriptor(),
				Responses: []ToolResponse{
					{Error: "temporary", Retryable: true},
					{Output: map[string]interface{}{"ok": true}},
				},
			}},
			Expected: ExpectedOutcome{ToolCalls: []ExpectedToolCall{
				{Name: "erp.lookup", Arguments: map[string]interface{}{}},
				{Name: "erp.lookup", Arguments: map[string]interface{}{}},
			}},
		}},
	}, version)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Cases[0].Passed || report.Cases[0].RetryCount != 1 ||
		report.Metrics.RetryRate.Value != 1 {
		t.Fatalf("unexpected retry metrics: %+v", report)
	}
}

func evaluationWorkflow(tenantID string) workflow.Version {
	key := workflow.Value{Ref: "input.invoice.id"}
	return workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow:      workflow.Workflow{ID: "invoice", TenantID: tenantID, Name: "Invoice"},
		Version:       1, Status: workflow.VersionPublished, CreatedAt: time.Now(),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"invoice": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":    map[string]interface{}{"type": "string"},
						"po_id": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		Steps: []workflow.Step{
			{
				ID: "lookup", Kind: workflow.StepTool,
				Tool: &workflow.ToolCall{
					Name: "erp.lookup_po",
					Arguments: map[string]workflow.Value{
						"po_id": {Ref: "input.invoice.po_id"},
					},
				},
			},
			{
				ID: "post", Kind: workflow.StepTool, DependsOn: []string{"lookup"},
				Tool: &workflow.ToolCall{
					Name: "erp.post_invoice",
					Arguments: map[string]workflow.Value{
						"invoice_id": {Ref: "input.invoice.id"},
					},
					IdempotencyKey: &key,
				},
			},
		},
	}
}

func safeReadDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskLow, SideEffect: core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
	}
}

func highRiskDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
		Idempotency: core.IdempotencyRequired,
	}
}
