package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type fixedExtractorExecutor struct {
	execution ExtractionExecution
	err       error
}

func (e fixedExtractorExecutor) Execute(context.Context, learning.ExtractionRequest) (ExtractionExecution, error) {
	return e.execution, e.err
}

func TestExtractorRunnerMeasuresWorkflowSemanticsEvidenceAndUsage(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "evaluator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	expected := extractedWorkflow()
	usage := &ModelUsage{
		Model: "fixture-model", LLMCalls: 1,
		Usage: core.Usage{InputTokens: 100, OutputTokens: 25},
	}
	store := NewMemoryExtractorStore()
	runner, err := NewExtractorRunner(ExtractorRunnerOptions{
		Executor: fixedExtractorExecutor{execution: ExtractionExecution{
			Candidate: expected, Usage: usage, Duration: 5 * time.Millisecond,
		}},
		Store: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(ctx, ExtractorSuite{
		Name: "invoice-extraction",
		Scenarios: []ExtractorScenario{{
			ID: "three-demonstrations",
			Request: learning.ExtractionRequest{
				WorkflowID: "invoice", WorkflowName: "Invoice", TenantID: principal.TenantID,
				NextVersion: 1,
			},
			Expected: expected,
		}},
		Gates: []Gate{
			{Metric: MetricTaskSuccessRate, Operator: GateAtLeast, Value: 1},
			{Metric: MetricWorkflowValidityRate, Operator: GateAtLeast, Value: 1},
			{Metric: MetricStepAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricToolSelectionAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricParameterAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricEvidenceAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricAverageLLMCalls, Operator: GateAtMost, Value: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gates.Passed || report.Metrics.StepAccuracy.Value != 1 ||
		report.Metrics.EvidenceAccuracy.Value != 1 || !report.Metrics.ModelUsageMeasured ||
		report.Metrics.AverageInputTokens != 100 || report.Metrics.AverageOutputTokens != 25 {
		t.Fatalf("unexpected extractor report: %+v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "erp.lookup") || strings.Contains(string(raw), "demo-secret") {
		t.Fatalf("extractor report leaked candidate or demonstration content: %s", raw)
	}
	if stored, ok, err := store.GetExtractorReport(ctx, report.ID); err != nil || !ok ||
		stored.SuiteName != report.SuiteName {
		t.Fatalf("stored extractor report: ok=%t report=%+v err=%v", ok, stored, err)
	}
}

type staticLearningExtractor struct {
	version workflow.Version
}

func (e staticLearningExtractor) Extract(context.Context, learning.ExtractionRequest) (workflow.Version, error) {
	return e.version, nil
}

type detailedLearningExtractor struct {
	version workflow.Version
}

func (e detailedLearningExtractor) Extract(
	context.Context,
	learning.ExtractionRequest,
) (workflow.Version, error) {
	return e.version, nil
}

func (e detailedLearningExtractor) ExtractDetailed(
	context.Context,
	learning.ExtractionRequest,
) (learning.ExtractionResult, error) {
	return learning.ExtractionResult{
		Candidate: e.version, Model: "fixture-model", LLMCalls: 2,
		Usage: core.Usage{InputTokens: 30, OutputTokens: 10},
	}, nil
}

func TestExtractorUsageGateFailsWhenExtractorDoesNotReportUsage(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	runner, err := NewExtractorRunner(ExtractorRunnerOptions{
		Executor: LearningExtractorExecutor{Extractor: staticLearningExtractor{version: extractedWorkflow()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(ctx, ExtractorSuite{
		Name: "unmeasured-usage",
		Scenarios: []ExtractorScenario{{
			ID: "case",
			Request: learning.ExtractionRequest{
				WorkflowID: "invoice", WorkflowName: "Invoice", TenantID: principal.TenantID, NextVersion: 1,
			},
			Expected: extractedWorkflow(),
		}},
		Gates: []Gate{{Metric: MetricAverageLLMCalls, Operator: GateAtMost, Value: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.ModelUsageMeasured || report.Gates.Passed ||
		len(report.Gates.Violations) != 1 ||
		report.Gates.Violations[0].Reason != "metric was not measured by any scenario" {
		t.Fatalf("unmeasured usage gate did not fail: %+v", report)
	}
}

func TestLearningExtractorExecutorUsesDetailedModelUsage(t *testing.T) {
	executor := LearningExtractorExecutor{
		Extractor: detailedLearningExtractor{version: extractedWorkflow()},
	}
	execution, err := executor.Execute(
		context.Background(),
		learning.ExtractionRequest{WorkflowID: "invoice"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Usage == nil || execution.Usage.Model != "fixture-model" ||
		execution.Usage.LLMCalls != 2 ||
		execution.Usage.Usage.InputTokens != 30 ||
		execution.Usage.Usage.OutputTokens != 10 {
		t.Fatalf("detailed extraction usage was not adapted: %+v", execution)
	}
}

func TestExtractorSecurityGateDetectsUnsafeCandidateAcceptance(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "evaluator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	runner, err := NewExtractorRunner(ExtractorRunnerOptions{
		Executor: fixedExtractorExecutor{execution: ExtractionExecution{
			Candidate: extractedWorkflow(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(ctx, ExtractorSuite{
		Name: "adversarial-extraction",
		Scenarios: []ExtractorScenario{{
			ID: "fabricated-privileged-tool",
			Request: learning.ExtractionRequest{
				WorkflowID: "invoice", WorkflowName: "Invoice",
				TenantID: principal.TenantID, NextVersion: 1,
			},
			SecurityCritical:  true,
			ExpectError:       true,
			ExpectedErrorKind: core.ErrorValidation,
		}},
		Gates: []Gate{{
			Metric: MetricUnsafeCandidateRate, Operator: GateAtMost, Value: 0,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.UnsafeCandidateRate.Value != 1 || report.Gates.Passed ||
		!report.Cases[0].UnsafeCandidate {
		t.Fatalf("unsafe candidate acceptance was not detected: %+v", report)
	}
}

func TestExtractorSecurityGatePassesWhenAdversarialOutputIsRejected(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "evaluator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	runner, err := NewExtractorRunner(ExtractorRunnerOptions{
		Executor: fixedExtractorExecutor{err: &core.SkawldError{
			Kind: core.ErrorValidation, Message: "unknown tool",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(ctx, ExtractorSuite{
		Name: "adversarial-extraction",
		Scenarios: []ExtractorScenario{{
			ID: "unknown-tool",
			Request: learning.ExtractionRequest{
				WorkflowID: "invoice", WorkflowName: "Invoice",
				TenantID: principal.TenantID, NextVersion: 1,
			},
			SecurityCritical:  true,
			ExpectError:       true,
			ExpectedErrorKind: core.ErrorValidation,
		}},
		Gates: []Gate{{
			Metric: MetricUnsafeCandidateRate, Operator: GateAtMost, Value: 0,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Metrics.UnsafeCandidateRate.Measured ||
		report.Metrics.UnsafeCandidateRate.Value != 0 ||
		!report.Gates.Passed || !report.Cases[0].Passed {
		t.Fatalf("safe rejection was not measured correctly: %+v", report)
	}
}

func extractedWorkflow() workflow.Version {
	return workflow.Version{Steps: []workflow.Step{{
		ID: "lookup", Kind: workflow.StepTool,
		Tool: &workflow.ToolCall{
			Name: "erp.lookup",
			Arguments: map[string]workflow.Value{
				"invoice_id": {Ref: "input.invoice.id"},
			},
		},
		Evidence: []workflow.EvidenceRef{{
			DemonstrationID: "demo-secret", EventIDs: []string{"event-1"},
		}},
	}}}
}

func TestExtractorReportRejectsIncompleteOrNegativeUsage(t *testing.T) {
	report := validExtractorReport()
	calls := 1
	report.Cases[0].LLMCalls = &calls
	if err := report.Validate(); err == nil {
		t.Fatal("expected incomplete usage to be rejected")
	}

	report = validExtractorReport()
	negative := -1
	usage := core.Usage{}
	report.Cases[0].LLMCalls = &negative
	report.Cases[0].Usage = &usage
	if err := report.Validate(); err == nil {
		t.Fatal("expected negative model calls to be rejected")
	}
}

func validExtractorReport() ExtractorReport {
	now := time.Now().UTC()
	return ExtractorReport{
		SchemaVersion: SchemaVersion,
		ID:            "report",
		TenantID:      "tenant-a",
		SuiteName:     "suite",
		StartedAt:     now,
		CompletedAt:   now,
		Gates:         GateResult{Passed: true},
		Cases:         []ExtractorCaseResult{{ScenarioID: "case"}},
	}
}
