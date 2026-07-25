package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func main() {
	principal := core.Principal{TenantID: "demo-company", ActorID: "release-reviewer"}
	expected := invoiceCandidate()
	runner, err := evaluation.NewExtractorRunner(evaluation.ExtractorRunnerOptions{
		Executor: fixtureExtractor{candidate: expected},
	})
	if err != nil {
		log.Fatal(err)
	}
	report, err := runner.Run(core.WithPrincipal(context.Background(), principal), evaluation.ExtractorSuite{
		Name: "invoice-extractor-release",
		Scenarios: []evaluation.ExtractorScenario{{
			ID: "three-demonstrations",
			Request: learning.ExtractionRequest{
				WorkflowID:   "invoice-review",
				WorkflowName: "Invoice review",
				TenantID:     principal.TenantID,
				NextVersion:  1,
			},
			Expected: expected,
		}},
		Gates: []evaluation.Gate{
			{Metric: evaluation.MetricTaskSuccessRate, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricWorkflowValidityRate, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricStepAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricToolSelectionAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricParameterAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricEvidenceAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricAverageLLMCalls, Operator: evaluation.GateAtMost, Value: 1},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	output, _ := json.MarshalIndent(map[string]interface{}{
		"report_id":               report.ID,
		"passed":                  report.Gates.Passed,
		"workflow_validity_rate":  report.Metrics.WorkflowValidityRate.Value,
		"step_accuracy":           report.Metrics.StepAccuracy.Value,
		"tool_selection_accuracy": report.Metrics.ToolSelectionAccuracy.Value,
		"parameter_accuracy":      report.Metrics.ParameterAccuracy.Value,
		"evidence_accuracy":       report.Metrics.EvidenceAccuracy.Value,
		"average_llm_calls":       report.Metrics.AverageLLMCalls,
	}, "", "  ")
	fmt.Println(string(output))
}

type fixtureExtractor struct {
	candidate workflow.Version
}

func (e fixtureExtractor) Execute(
	context.Context,
	learning.ExtractionRequest,
) (evaluation.ExtractionExecution, error) {
	return evaluation.ExtractionExecution{
		Candidate: e.candidate,
		Usage: &evaluation.ModelUsage{
			Model:    "fixture-extractor",
			LLMCalls: 1,
			Usage:    core.Usage{InputTokens: 120, OutputTokens: 35},
		},
		Duration: 4 * time.Millisecond,
	}, nil
}

func invoiceCandidate() workflow.Version {
	return workflow.Version{
		Steps: []workflow.Step{{
			ID:   "lookup_invoice",
			Kind: workflow.StepTool,
			Tool: &workflow.ToolCall{
				Name: "accounting.lookup_invoice",
				Arguments: map[string]workflow.Value{
					"invoice_id": {Ref: "input.invoice.id"},
				},
			},
			Evidence: []workflow.EvidenceRef{{
				DemonstrationID: "demo-1",
				EventIDs:        []string{"event-1"},
			}},
		}},
	}
}
