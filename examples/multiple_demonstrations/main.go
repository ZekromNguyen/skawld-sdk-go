package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type fixtureExtractor struct{}

func (fixtureExtractor) Extract(_ context.Context, request learning.ExtractionRequest) (workflow.Version, error) {
	lookupEvidence := make([]workflow.EvidenceRef, 0, len(request.Demonstrations))
	finalizeEvidence := make([]workflow.EvidenceRef, 0, len(request.Demonstrations))
	for _, demo := range request.Demonstrations {
		lookupEvidence = append(lookupEvidence, workflow.EvidenceRef{
			DemonstrationID: demo.ID, EventIDs: []string{demo.Trace.Events[0].ID},
		})
		finalizeEvidence = append(finalizeEvidence, workflow.EvidenceRef{
			DemonstrationID: demo.ID, EventIDs: []string{demo.Trace.Events[len(demo.Trace.Events)-1].ID},
		})
	}
	idempotencyKey := workflow.Value{Ref: "input.invoice_id"}
	return workflow.Version{
		Steps: []workflow.Step{
			{
				ID: "lookup_invoice", Kind: workflow.StepTool, Evidence: lookupEvidence,
				Tool: &workflow.ToolCall{
					Name: "accounting.lookup_invoice",
					Arguments: map[string]workflow.Value{
						"invoice_id": {Ref: "input.invoice_id"},
					},
				},
			},
			{
				ID: "finalize_review", Kind: workflow.StepTool,
				DependsOn: []string{"lookup_invoice"}, Evidence: finalizeEvidence,
				Tool: &workflow.ToolCall{
					Name: "accounting.finalize_review",
					Arguments: map[string]workflow.Value{
						"invoice_id": {Ref: "input.invoice_id"},
					},
					IdempotencyKey: &idempotencyKey,
				},
			},
		}}, nil
}

type fixtureCatalog struct{}

func (fixtureCatalog) Describe(_ context.Context, name string) (core.ToolDescriptor, bool, error) {
	switch name {
	case "accounting.lookup_invoice":
		return core.ToolDescriptor{
			Risk: core.RiskLow, SideEffect: core.SideEffectNone,
			Idempotency: core.IdempotencyNotApplicable,
		}, true, nil
	case "accounting.finalize_review":
		return core.ToolDescriptor{
			Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
			Idempotency: core.IdempotencyRequired,
		}, true, nil
	default:
		return core.ToolDescriptor{}, false, nil
	}
}

func (fixtureCatalog) ToolCatalogFingerprint(
	context.Context,
	[]string,
) (string, error) {
	return "fixture-accounting-tools-v1", nil
}

func main() {
	principal := core.Principal{TenantID: "demo-company", ActorID: "workflow-reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demonstrations := []observation.Demonstration{
		demonstration(principal, "demo-1", "INV-1001", false),
		demonstration(principal, "demo-2", "INV-1002", true),
		demonstration(principal, "demo-3", "INV-1003", false),
	}
	compiler := learning.Compiler{
		Extractor: fixtureExtractor{},
		Tools:     fixtureCatalog{},
		Store:     workflow.NewMemoryStore(),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"invoice_id": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"invoice_id"},
		},
	}
	result, err := compiler.CompileMultiple(
		ctx, "invoice-review", "Invoice review", demonstrations, learning.MultiDemoOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	output, _ := json.MarshalIndent(map[string]interface{}{
		"candidate_version":      result.Candidate.Version,
		"candidate_status":       result.Candidate.Status,
		"demonstrations":         result.Candidate.Learning.DemonstrationCount,
		"sequence_consistency":   result.Candidate.Learning.SequenceConsistency,
		"sequence_variants":      len(result.Analysis.SequenceVariants),
		"parameter_candidates":   result.Candidate.Learning.ParameterCandidateCount,
		"step_evidence_coverage": result.Candidate.Learning.StepEvidenceCoverage,
		"requires_human_review":  result.Candidate.Learning.RequiresHumanReview,
	}, "", "  ")
	fmt.Println(string(output))
}

func demonstration(principal core.Principal, id, invoiceID string, requestedReview bool) observation.Demonstration {
	events := []observation.Event{
		semanticEvent(principal, id, "lookup", "lookup_invoice", map[string]interface{}{"invoice_id": invoiceID}),
	}
	if requestedReview {
		events = append(events, semanticEvent(
			principal, id, "review", "request_human_review",
			map[string]interface{}{"invoice_id": invoiceID, "reason": "amount_mismatch"},
		))
	}
	events = append(events, semanticEvent(
		principal, id, "finalize", "finalize_review", map[string]interface{}{"invoice_id": invoiceID},
	))
	for index := range events {
		events[index].Timestamp = time.Unix(int64(index+1), 0).UTC()
	}
	return observation.Demonstration{
		ID: id, WorkflowKey: "invoice-review", Principal: principal,
		Status: observation.DemonstrationCompleted,
		Trace: observation.WorkflowTrace{
			SchemaVersion: observation.SchemaVersion, SessionID: id + "-session", Events: events,
		},
	}
}

func semanticEvent(
	principal core.Principal,
	demonstrationID, suffix, action string,
	input map[string]interface{},
) observation.Event {
	return observation.Event{
		SchemaVersion: observation.SchemaVersion,
		ID:            demonstrationID + "-" + suffix, SessionID: demonstrationID + "-session",
		Principal: principal, Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent,
		Application: "accounting", Action: action, Entity: &observation.Entity{Type: "invoice"},
		Input: input,
	}
}
