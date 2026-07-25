package learning

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
)

func TestAnalyzeFindsCommonActionsVariablesAndSequenceVariation(t *testing.T) {
	demos := []observation.Demonstration{
		completedDemo("demo-1",
			event("d1-open", "open_invoice", map[string]interface{}{"invoice_id": "SECRET-1001", "currency": "USD"}),
			event("d1-approve", "approve_invoice", map[string]interface{}{"invoice_id": "SECRET-1001"}),
		),
		completedDemo("demo-2",
			event("d2-open", "open_invoice", map[string]interface{}{"invoice_id": "SECRET-1002", "currency": "USD"}),
			event("d2-review", "request_review", map[string]interface{}{"reason": "amount_mismatch"}),
			event("d2-approve", "approve_invoice", map[string]interface{}{"invoice_id": "SECRET-1002"}),
		),
		completedDemo("demo-3",
			event("d3-open", "open_invoice", map[string]interface{}{"invoice_id": "SECRET-1003", "currency": "USD"}),
			event("d3-approve", "approve_invoice", map[string]interface{}{"invoice_id": "SECRET-1003"}),
		),
	}

	analysis, err := Analyze(demos, AnalyzerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.SequenceVariants) != 2 {
		t.Fatalf("sequence variants = %d, want 2", len(analysis.SequenceVariants))
	}
	if math.Abs(analysis.SequenceConsistency-(7.0/9.0)) > 0.0001 {
		t.Fatalf("sequence consistency = %f, want %f", analysis.SequenceConsistency, 7.0/9.0)
	}

	var commonOpen, lowSupportReview bool
	for _, action := range analysis.Actions {
		switch action.Signature.Action {
		case "open_invoice":
			commonOpen = action.Common && action.DemonstrationSupport == 1
		case "request_review":
			lowSupportReview = !action.Common && action.DemonstrationSupport == 1.0/3.0
		}
	}
	if !commonOpen || !lowSupportReview {
		t.Fatalf("unexpected action support: %+v", analysis.Actions)
	}

	var invoiceVariable, currencyConstant bool
	for _, parameter := range analysis.Parameters {
		if parameter.Action.Action != "open_invoice" || parameter.Location != "input" {
			continue
		}
		switch parameter.Path {
		case "invoice_id":
			invoiceVariable = parameter.Classification == ParameterVariable && parameter.DistinctValueCount == 3
		case "currency":
			currencyConstant = parameter.Classification == ParameterConstant && parameter.DistinctValueCount == 1
		}
	}
	if !invoiceVariable || !currencyConstant {
		t.Fatalf("unexpected parameter analysis: %+v", analysis.Parameters)
	}

	raw, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET-") {
		t.Fatalf("analysis leaked observed values: %s", raw)
	}
}

func TestAnalyzeRejectsMixedWorkflowKeys(t *testing.T) {
	first := completedDemo("demo-1", event("event-1", "open", nil))
	second := completedDemo("demo-2", event("event-2", "open", nil))
	second.WorkflowKey = "another-workflow"
	if _, err := Analyze([]observation.Demonstration{first, second}, AnalyzerOptions{}); err == nil {
		t.Fatal("expected mixed workflow keys to be rejected")
	}
}

func TestAnalyzeFlagsAmbiguousTransitionWithMatchingObservedState(t *testing.T) {
	demos := []observation.Demonstration{
		completedDemo("demo-1",
			event("d1-check", "check_invoice", map[string]interface{}{"status": "pending"}),
			event("d1-approve", "approve_invoice", nil),
		),
		completedDemo("demo-2",
			event("d2-check", "check_invoice", map[string]interface{}{"status": "pending"}),
			event("d2-reject", "reject_invoice", nil),
		),
	}
	analysis, err := Analyze(demos, AnalyzerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Conflicts) != 1 || analysis.Conflicts[0].Kind != "ambiguous_transition" {
		t.Fatalf("unexpected conflicts: %+v", analysis.Conflicts)
	}
}

func TestAnalyzeFindsValueFreeBranchDiscriminatorEvidence(t *testing.T) {
	demos := []observation.Demonstration{
		completedDemo(
			"demo-1",
			event(
				"d1-check", "check_invoice",
				map[string]interface{}{"amount": 5000.0},
			),
			event("d1-approve", "approve_invoice", nil),
		),
		completedDemo(
			"demo-2",
			event(
				"d2-check", "check_invoice",
				map[string]interface{}{"amount": 15000.0},
			),
			event("d2-review", "request_manager_review", nil),
		),
	}
	analysis, err := Analyze(demos, AnalyzerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.BranchCandidates) != 1 {
		t.Fatalf(
			"branch candidates = %+v, want one amount discriminator",
			analysis.BranchCandidates,
		)
	}
	candidate := analysis.BranchCandidates[0]
	if candidate.Location != "input" || candidate.Path != "amount" ||
		candidate.OutcomeCount != 2 || len(candidate.Evidence) != 2 {
		t.Fatalf("unexpected branch candidate: %+v", candidate)
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "5000") ||
		strings.Contains(string(raw), "15000") {
		t.Fatalf("branch evidence leaked observed values: %s", raw)
	}
}

func completedDemo(id string, events ...observation.Event) observation.Demonstration {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	for index := range events {
		events[index].SchemaVersion = observation.SchemaVersion
		events[index].SessionID = id + "-session"
		events[index].Principal = principal
		events[index].Timestamp = time.Unix(int64(index+1), 0).UTC()
	}
	return observation.Demonstration{
		ID: id, WorkflowKey: "invoice-review", Principal: principal,
		Status: observation.DemonstrationCompleted,
		Trace: observation.WorkflowTrace{
			SchemaVersion: observation.SchemaVersion,
			SessionID:     id + "-session",
			Events:        events,
		},
	}
}

func event(id, action string, input map[string]interface{}) observation.Event {
	return observation.Event{
		ID: id, Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent,
		Application: "accounting", Action: action, Entity: &observation.Entity{Type: "invoice"},
		Input: input,
	}
}
