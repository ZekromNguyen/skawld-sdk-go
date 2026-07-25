package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestResolverUsesExactPublishedRoute(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "publisher"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store := NewMemoryStore()
	candidate, err := store.SaveCandidate(ctx, Version{
		SchemaVersion: SchemaVersion,
		Workflow:      Workflow{ID: "invoice", TenantID: principal.TenantID, Name: "Invoice"},
		Version:       1, Status: VersionCandidate, CreatedAt: time.Now(),
		Steps: []Step{{ID: "approve", Kind: StepApproval, Approval: &ApprovalSpec{
			Summary: "Approve invoice", Risk: core.RiskHigh,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, candidate.Workflow.ID, candidate.Version, principal)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(ResolverOptions{
		Store: store, Routes: []Route{{TaskType: "invoice.process", WorkflowID: "invoice"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(ctx, ResolutionRequest{TaskType: "invoice.process"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Workflow.ID != published.Workflow.ID || resolved.Version != published.Version {
		t.Fatalf("resolved %+v, want %+v", resolved, published)
	}
}

func TestResolverRejectsUnknownConflictAndCrossTenantAccess(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "publisher"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store := NewMemoryStore()
	candidate, err := store.SaveCandidate(ctx, Version{
		SchemaVersion: SchemaVersion,
		Workflow:      Workflow{ID: "invoice", TenantID: principal.TenantID, Name: "Invoice"},
		Version:       1, Status: VersionCandidate, CreatedAt: time.Now(),
		Steps: []Step{{ID: "review", Kind: StepApproval, Approval: &ApprovalSpec{
			Summary: "Review", Risk: core.RiskHigh,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, "invoice", candidate.Version, principal); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(ResolverOptions{
		Store: store, Routes: []Route{{TaskType: "invoice.process", WorkflowID: "invoice"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx, ResolutionRequest{TaskType: "missing"}); !errors.Is(err, &core.SkawldError{Kind: core.ErrorNotFound}) {
		t.Fatalf("unknown route error = %v", err)
	}
	if _, err := resolver.Resolve(ctx, ResolutionRequest{
		TaskType: "invoice.process", WorkflowID: "other",
	}); !errors.Is(err, &core.SkawldError{Kind: core.ErrorConflict}) {
		t.Fatalf("route conflict error = %v", err)
	}
	otherTenant := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-b", ActorID: "reader",
	})
	// Resolution deliberately does not disclose cross-tenant workflow
	// existence.
	if _, err := resolver.Resolve(otherTenant, ResolutionRequest{WorkflowID: "invoice"}); !errors.Is(err, &core.SkawldError{Kind: core.ErrorNotFound}) {
		t.Fatalf("cross-tenant error = %v", err)
	}
}

func TestResolverRejectsDuplicateRoutes(t *testing.T) {
	_, err := NewResolver(ResolverOptions{
		Store: NewMemoryStore(),
		Routes: []Route{
			{TaskType: "invoice.process", WorkflowID: "invoice-v1"},
			{TaskType: "invoice.process", WorkflowID: "invoice-v2"},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate route to fail")
	}
}
