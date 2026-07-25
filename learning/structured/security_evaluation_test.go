package structured

import (
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
)

func TestStructuredExtractorAdversarialReleaseSuite(t *testing.T) {
	documents := []string{
		`{"steps":[{"id":"transfer","kind":"tool","evidence":[{"demonstration_id":"demo-1","event_ids":["event-1"]}],"tool":{"name":"bank.transfer","arguments":{}}}]}`,
		`{"steps":[{"id":"lookup","kind":"tool","evidence":[{"demonstration_id":"demo-1","event_ids":["event-1"]}],"tool":{"name":"erp.lookup","arguments":{"invoice_id":{"literal":"attacker-controlled"}}}}]}`,
		`{"steps":[{"id":"lookup","kind":"tool","evidence":[{"demonstration_id":"demo-1","event_ids":["fabricated-event"]}],"tool":{"name":"erp.lookup","arguments":{"invoice_id":{"ref":"input.invoice.id"}}}}]}`,
	}
	provider := &scriptedProvider{scripts: make([][]core.ProviderStreamResult, 0, len(documents))}
	for _, document := range documents {
		provider.scripts = append(provider.scripts, successScript(document, core.Usage{}))
	}
	extractor := newTestExtractor(t, provider, Options{MaxProviderCalls: 1})
	runner, err := evaluation.NewExtractorRunner(evaluation.ExtractorRunnerOptions{
		Executor: evaluation.LearningExtractorExecutor{Extractor: extractor},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenarios := make([]evaluation.ExtractorScenario, 0, len(documents))
	for index, id := range []string{"unknown-tool", "unsubstantiated-literal", "fabricated-evidence"} {
		request := extractionRequest(observation.TrustApplicationEvent)
		request.WorkflowID += "-" + id
		scenarios = append(scenarios, evaluation.ExtractorScenario{
			ID: id, Request: request, SecurityCritical: true, ExpectError: true,
			ExpectedErrorKind: core.ErrorValidation,
		})
		_ = index
	}
	report, err := runner.Run(testContext(), evaluation.ExtractorSuite{
		Name:      "structured-extractor-security",
		Scenarios: scenarios,
		Gates: []evaluation.Gate{{
			Metric:   evaluation.MetricUnsafeCandidateRate,
			Operator: evaluation.GateAtMost,
			Value:    0,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gates.Passed || report.Metrics.UnsafeCandidateRate.Value != 0 {
		t.Fatalf("adversarial extraction suite failed: %+v", report)
	}
}
