package learning

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type fakeExtractor struct {
	version workflow.Version
}

func (f fakeExtractor) Extract(context.Context, ExtractionRequest) (workflow.Version, error) {
	return f.version, nil
}

type fakeCatalog struct {
	known bool
}

func (c fakeCatalog) Describe(context.Context, string) (core.ToolDescriptor, bool, error) {
	return core.ToolDescriptor{
		Risk: core.RiskLow, SideEffect: core.SideEffectNone, Idempotency: core.IdempotencyNotApplicable,
	}, c.known, nil
}

func (c fakeCatalog) ToolCatalogFingerprint(context.Context, []string) (string, error) {
	return "test-catalog", nil
}

func TestCompilerNormalizesUntrustedExtractionAsCandidate(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demo := observation.Demonstration{
		ID: "demo-1", Principal: principal, Status: observation.DemonstrationCompleted,
		Trace: observation.WorkflowTrace{
			SchemaVersion: observation.SchemaVersion, SessionID: "trace-1",
			Events: []observation.Event{{
				SchemaVersion: observation.SchemaVersion, ID: "event-1", SessionID: "trace-1",
				Principal: principal, Timestamp: time.Now(), Source: observation.SourceAPI,
				Trust: observation.TrustApplicationEvent, Action: "lookup",
			}},
		},
	}
	store := workflow.NewMemoryStore()
	compiler := Compiler{
		Extractor: fakeExtractor{version: workflow.Version{
			Status: workflow.VersionPublished, PublishedAt: time.Now(), PublishedBy: "extractor",
			Learning: &workflow.LearningMetadata{
				DemonstrationCount: 999, SequenceConsistency: 1, StepEvidenceCoverage: 1,
			},
			Steps: []workflow.Step{{
				ID: "lookup", Kind: workflow.StepTool, Tool: &workflow.ToolCall{Name: "erp.lookup"},
			}},
		}},
		Tools: fakeCatalog{known: true}, Store: store,
	}
	candidate, err := compiler.Compile(ctx, "invoice", "Invoice", []observation.Demonstration{demo})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Status != workflow.VersionCandidate || candidate.Version != 1 ||
		candidate.Workflow.TenantID != principal.TenantID || len(candidate.SourceDemonstrationIDs) != 1 ||
		candidate.Learning != nil || !candidate.PublishedAt.IsZero() || candidate.PublishedBy != "" {
		t.Fatalf("extracted candidate was not normalized: %+v", candidate)
	}
	if _, ok, err := store.Published(ctx, "invoice"); err != nil || ok {
		t.Fatalf("compiler must not publish extracted output: ok=%t err=%v", ok, err)
	}
}

func TestCompilerRejectsFabricatedEvidenceInSingleDemonstrationMode(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demo := observation.Demonstration{
		ID: "demo", Principal: principal, Status: observation.DemonstrationCompleted,
		Trace: observation.WorkflowTrace{
			SchemaVersion: observation.SchemaVersion, SessionID: "trace",
			Events: []observation.Event{{
				SchemaVersion: observation.SchemaVersion, ID: "event", SessionID: "trace",
				Principal: principal, Timestamp: time.Now(), Source: observation.SourceAPI,
				Trust: observation.TrustApplicationEvent, Action: "lookup",
			}},
		},
	}
	compiler := Compiler{
		Extractor: fakeExtractor{version: workflow.Version{Steps: []workflow.Step{{
			ID: "lookup", Kind: workflow.StepTool, Tool: &workflow.ToolCall{Name: "erp.lookup"},
			Evidence: []workflow.EvidenceRef{{DemonstrationID: "demo", EventIDs: []string{"fabricated"}}},
		}}}},
		Tools: fakeCatalog{known: true}, Store: workflow.NewMemoryStore(),
	}
	if _, err := compiler.Compile(ctx, "workflow", "Workflow", []observation.Demonstration{demo}); err == nil {
		t.Fatal("expected fabricated evidence to be rejected")
	}
}

func TestCompilerRejectsUnknownExtractedTool(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demo := observation.Demonstration{
		ID: "demo", Principal: principal, Status: observation.DemonstrationCompleted,
		Trace: observation.WorkflowTrace{
			SchemaVersion: observation.SchemaVersion, SessionID: "trace",
			Events: []observation.Event{{
				SchemaVersion: observation.SchemaVersion, ID: "event", SessionID: "trace",
				Principal: principal, Timestamp: time.Now(), Source: observation.SourceAPI,
				Trust: observation.TrustApplicationEvent, Action: "unsafe",
			}},
		},
	}
	compiler := Compiler{
		Extractor: fakeExtractor{version: workflow.Version{Steps: []workflow.Step{{
			ID: "unsafe", Kind: workflow.StepTool, Tool: &workflow.ToolCall{Name: "unknown"},
		}}}},
		Tools: fakeCatalog{known: false}, Store: workflow.NewMemoryStore(),
	}
	if _, err := compiler.Compile(ctx, "workflow", "Workflow", []observation.Demonstration{demo}); err == nil {
		t.Fatal("expected unknown extracted tool to be rejected")
	}
}

func TestCompilerRejectsExtractorReferenceOutsideTrustedContract(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demo := observation.Demonstration{
		ID: "demo", WorkflowKey: "invoice", Principal: principal,
		Status: observation.DemonstrationCompleted, CompletedAt: time.Now(),
		Trace: observation.WorkflowTrace{
			SchemaVersion: observation.SchemaVersion, SessionID: "trace",
			Events: []observation.Event{{
				SchemaVersion: observation.SchemaVersion, ID: "event", SessionID: "trace",
				Principal: principal, Timestamp: time.Now(), Source: observation.SourceAPI,
				Trust: observation.TrustApplicationEvent, Action: "lookup",
			}},
		},
	}
	compiler := Compiler{
		Extractor: fakeExtractor{version: workflow.Version{Steps: []workflow.Step{{
			ID: "lookup", Kind: workflow.StepTool,
			Tool: &workflow.ToolCall{
				Name: "erp.lookup",
				Arguments: map[string]workflow.Value{
					"id": {Ref: "input.invoice.secret"},
				},
			},
		}}}},
		Tools: fakeCatalog{known: true}, Store: workflow.NewMemoryStore(),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"invoice": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	}
	if _, err := compiler.Compile(
		ctx, "invoice", "Invoice", []observation.Demonstration{demo},
	); err == nil {
		t.Fatal("expected extractor reference outside trusted contract to be rejected")
	}
}
