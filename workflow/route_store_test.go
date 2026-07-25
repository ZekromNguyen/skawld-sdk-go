package workflow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestMemoryRouteStoreUsesOptimisticRevisionsAndTenantIsolation(t *testing.T) {
	store := NewMemoryRouteStore()
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	created, err := store.Save(ctx, Route{
		TaskType: "invoice.reconcile", WorkflowID: "invoice-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.TenantID != principal.TenantID ||
		created.UpdatedBy != principal.ActorID || created.UpdatedAt.IsZero() {
		t.Fatalf("unexpected created route: %+v", created)
	}
	stale := created
	updated := created
	updated.WorkflowID = "invoice-v2"
	updated, err = store.Save(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.WorkflowID != "invoice-v2" {
		t.Fatalf("unexpected updated route: %+v", updated)
	}
	if _, err := store.Save(ctx, stale); !errors.Is(
		err, &core.SkawldError{Kind: core.ErrorConflict},
	) {
		t.Fatalf("stale route update error = %v", err)
	}
	otherTenant := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-b", ActorID: "operator-b",
	})
	if _, exists, err := store.Get(
		otherTenant, created.TaskType,
	); err != nil || exists {
		t.Fatalf("cross-tenant route leaked: exists=%t err=%v", exists, err)
	}
	if err := store.Delete(ctx, updated.TaskType, stale.Revision); !errors.Is(
		err, &core.SkawldError{Kind: core.ErrorConflict},
	) {
		t.Fatalf("stale route delete error = %v", err)
	}
	if err := store.Delete(ctx, updated.TaskType, updated.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestResolverUsesTenantScopedRouteStore(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	workflows := NewMemoryStore()
	candidate, err := workflows.SaveCandidate(ctx, Version{
		SchemaVersion: SchemaVersion,
		Workflow: Workflow{
			ID: "invoice", TenantID: principal.TenantID, Name: "Invoice",
		},
		Version: 1, Status: VersionCandidate,
		Steps: []Step{{
			ID: "review", Kind: StepApproval,
			Approval: &ApprovalSpec{Summary: "Review", Risk: core.RiskHigh},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflows.Publish(
		ctx, candidate.Workflow.ID, candidate.Version, principal,
	); err != nil {
		t.Fatal(err)
	}
	routes := NewMemoryRouteStore()
	if _, err := routes.Save(ctx, Route{
		TaskType: "invoice.reconcile", WorkflowID: "invoice",
	}); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(ResolverOptions{
		Store: workflows, RouteStore: routes,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(ctx, ResolutionRequest{
		TaskType: "invoice.reconcile",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Workflow.ID != "invoice" || resolved.Version != 1 {
		t.Fatalf("unexpected resolved version: %+v", resolved)
	}
}

func TestMemoryRouteStoreAllowsOneConcurrentRevisionWinner(t *testing.T) {
	store := NewMemoryRouteStore()
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	created, err := store.Save(ctx, Route{
		TaskType: "invoice.reconcile", WorkflowID: "invoice-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var successes, conflicts atomic.Int64
	var workers sync.WaitGroup
	for _, workflowID := range []string{"invoice-v2", "invoice-v3"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			update := created
			update.WorkflowID = workflowID
			if _, err := store.Save(ctx, update); err == nil {
				successes.Add(1)
			} else if errors.Is(err, &core.SkawldError{Kind: core.ErrorConflict}) {
				conflicts.Add(1)
			} else {
				t.Errorf("unexpected concurrent update error: %v", err)
			}
		}()
	}
	workers.Wait()
	if successes.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf(
			"concurrent updates: successes=%d conflicts=%d",
			successes.Load(), conflicts.Load(),
		)
	}
}
