package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func main() {
	principal := core.Principal{TenantID: "demo-company", ActorID: "release-reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	version := invoiceWorkflow(principal.TenantID)
	suite := evaluation.Suite{
		Name: "invoice-release-gate",
		Scenarios: []evaluation.Scenario{{
			ID: "matching-purchase-order",
			Input: map[string]interface{}{
				"invoice": map[string]interface{}{"id": "INV-1001", "po_id": "PO-42"},
			},
			Tools: map[string]evaluation.ToolFixture{
				"accounting.lookup_purchase_order": {
					Descriptor: core.ToolDescriptor{
						Risk: core.RiskLow, SideEffect: core.SideEffectNone,
						Idempotency: core.IdempotencyNotApplicable,
					},
					Responses: []evaluation.ToolResponse{{
						Output: map[string]interface{}{"id": "PO-42", "total": 500.0},
					}},
				},
				"accounting.mark_invoice_reviewed": {
					Descriptor: core.ToolDescriptor{
						Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
						Idempotency: core.IdempotencyRequired,
					},
					Responses: []evaluation.ToolResponse{{
						Output: map[string]interface{}{"status": "reviewed"},
					}},
				},
			},
			Approvals: map[string]policy.ApprovalStatus{
				"mark_reviewed": policy.ApprovalGranted,
			},
			Expected: evaluation.ExpectedOutcome{
				Status: workflow.ExecutionCompleted,
				ToolCalls: []evaluation.ExpectedToolCall{
					{
						Name:      "accounting.lookup_purchase_order",
						Arguments: map[string]interface{}{"po_id": "PO-42"},
					},
					{
						Name:      "accounting.mark_invoice_reviewed",
						Arguments: map[string]interface{}{"invoice_id": "INV-1001"},
					},
				},
			},
		}},
		Gates: []evaluation.Gate{
			{Metric: evaluation.MetricTaskSuccessRate, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricToolSelectionAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricParameterAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricUnsafeActionRate, Operator: evaluation.GateAtMost, Value: 0},
			{Metric: evaluation.MetricAverageLLMCalls, Operator: evaluation.GateAtMost, Value: 0},
		},
	}
	report, err := evaluation.NewRunner(evaluation.RunnerOptions{}).Run(ctx, suite, version)
	if err != nil {
		log.Fatal(err)
	}
	output, _ := json.MarshalIndent(map[string]interface{}{
		"report_id":               report.ID,
		"passed":                  report.Gates.Passed,
		"task_success_rate":       report.Metrics.TaskSuccessRate.Value,
		"tool_selection_accuracy": report.Metrics.ToolSelectionAccuracy.Value,
		"parameter_accuracy":      report.Metrics.ParameterAccuracy.Value,
		"unsafe_action_rate":      report.Metrics.UnsafeActionRate.Value,
		"average_llm_calls":       report.Metrics.AverageLLMCalls,
	}, "", "  ")
	fmt.Println(string(output))
}

func invoiceWorkflow(tenantID string) workflow.Version {
	idempotencyKey := workflow.Value{Ref: "input.invoice.id"}
	return workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow: workflow.Workflow{
			ID: "invoice-reconciliation", TenantID: tenantID, Name: "Invoice reconciliation",
		},
		Version: 1, Status: workflow.VersionPublished, CreatedAt: time.Now().UTC(),
		Steps: []workflow.Step{
			{
				ID: "lookup_po", Kind: workflow.StepTool,
				Tool: &workflow.ToolCall{
					Name: "accounting.lookup_purchase_order",
					Arguments: map[string]workflow.Value{
						"po_id": {Ref: "input.invoice.po_id"},
					},
				},
			},
			{
				ID: "mark_reviewed", Kind: workflow.StepTool, DependsOn: []string{"lookup_po"},
				Tool: &workflow.ToolCall{
					Name: "accounting.mark_invoice_reviewed",
					Arguments: map[string]workflow.Value{
						"invoice_id": {Ref: "input.invoice.id"},
					},
					IdempotencyKey: &idempotencyKey,
				},
			},
		},
	}
}
