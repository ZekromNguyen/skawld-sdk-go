package learning

import (
	"context"
	"errors"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type capturingExtractor struct {
	request ExtractionRequest
	version workflow.Version
}

func (e *capturingExtractor) Extract(_ context.Context, request ExtractionRequest) (workflow.Version, error) {
	e.request = request
	return e.version, nil
}

func TestCompileMultiplePersistsValidatedEvidenceAndLearningMetadata(t *testing.T) {
	demos := []observation.Demonstration{
		completedDemo("demo-1", event("event-1", "lookup_invoice", map[string]interface{}{"id": "INV-1"})),
		completedDemo("demo-2", event("event-2", "lookup_invoice", map[string]interface{}{"id": "INV-2"})),
	}
	extractor := &capturingExtractor{version: workflow.Version{Steps: []workflow.Step{{
		ID: "lookup", Kind: workflow.StepTool,
		Tool: &workflow.ToolCall{Name: "erp.lookup_invoice"},
		Evidence: []workflow.EvidenceRef{
			{DemonstrationID: "demo-1", EventIDs: []string{"event-1"}},
			{DemonstrationID: "demo-2", EventIDs: []string{"event-2"}},
		},
	}}}}
	store := workflow.NewMemoryStore()
	compiler := Compiler{Extractor: extractor, Tools: fakeCatalog{known: true}, Store: store}
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a", ActorID: "reviewer"})

	result, err := compiler.CompileMultiple(ctx, "invoice-review", "Invoice review", demos, MultiDemoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if extractor.request.Analysis == nil {
		t.Fatal("extractor did not receive deterministic analysis")
	}
	if result.Candidate.Status != workflow.VersionCandidate {
		t.Fatalf("candidate status = %q", result.Candidate.Status)
	}
	metadata := result.Candidate.Learning
	if metadata == nil || metadata.DemonstrationCount != 2 || metadata.SequenceConsistency != 1 ||
		metadata.StepEvidenceCoverage != 1 || !metadata.RequiresHumanReview {
		t.Fatalf("unexpected learning metadata: %+v", metadata)
	}
	stored, ok, err := store.Get(ctx, "invoice-review", 1)
	if err != nil || !ok {
		t.Fatalf("stored candidate: ok=%t err=%v", ok, err)
	}
	if stored.Learning == nil || len(stored.Steps[0].Evidence) != 2 {
		t.Fatalf("stored candidate lost provenance: %+v", stored)
	}

	extractor.version.Steps[0].Tool.Name = "erp.lookup_invoice_v2"
	second, err := compiler.CompileMultiple(ctx, "invoice-review", "Invoice review", demos, MultiDemoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Candidate.Version != 2 || second.Changes.BaseVersion != 1 ||
		len(second.Changes.Steps) != 1 || second.Changes.Steps[0].Kind != ChangeStepModified {
		t.Fatalf("unexpected iterative candidate change set: %+v", second)
	}
}

func TestCompileMultipleRejectsUnknownEvidence(t *testing.T) {
	demos := []observation.Demonstration{
		completedDemo("demo-1", event("event-1", "lookup_invoice", nil)),
		completedDemo("demo-2", event("event-2", "lookup_invoice", nil)),
	}
	extractor := &capturingExtractor{version: workflow.Version{Steps: []workflow.Step{{
		ID: "lookup", Kind: workflow.StepTool,
		Tool:     &workflow.ToolCall{Name: "erp.lookup_invoice"},
		Evidence: []workflow.EvidenceRef{{DemonstrationID: "demo-1", EventIDs: []string{"fabricated"}}},
	}}}}
	compiler := Compiler{Extractor: extractor, Tools: fakeCatalog{known: true}, Store: workflow.NewMemoryStore()}
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a"})

	_, err := compiler.CompileMultiple(ctx, "invoice-review", "Invoice review", demos, MultiDemoOptions{})
	if err == nil {
		t.Fatal("expected fabricated evidence to be rejected")
	}
	var skawldErr *core.SkawldError
	if !errors.As(err, &skawldErr) || skawldErr.Kind != core.ErrorValidation {
		t.Fatalf("expected validation error, got %T %v", err, err)
	}
}

func TestCompileMultipleRequiresEnoughDemonstrations(t *testing.T) {
	demo := completedDemo("demo-1", event("event-1", "lookup_invoice", nil))
	compiler := Compiler{
		Extractor: &capturingExtractor{}, Tools: fakeCatalog{known: true}, Store: workflow.NewMemoryStore(),
	}
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a"})
	if _, err := compiler.CompileMultiple(ctx, "invoice-review", "Invoice review", []observation.Demonstration{demo}, MultiDemoOptions{}); err == nil {
		t.Fatal("expected a single demonstration to be rejected")
	}
}

func TestCompileMultipleRejectsAmbiguousTransitionsByDefault(t *testing.T) {
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
	compiler := Compiler{
		Extractor: &capturingExtractor{}, Tools: fakeCatalog{known: true}, Store: workflow.NewMemoryStore(),
	}
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a"})
	_, err := compiler.CompileMultiple(ctx, "invoice-review", "Invoice review", demos, MultiDemoOptions{})
	if err == nil {
		t.Fatal("expected ambiguous transitions to be rejected")
	}
	var skawldErr *core.SkawldError
	if !errors.As(err, &skawldErr) || skawldErr.Kind != core.ErrorValidation {
		t.Fatalf("expected validation error, got %T %v", err, err)
	}
}
