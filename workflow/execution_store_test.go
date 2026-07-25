package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestMemoryExecutionStoreRevisionTenantAndMutationIsolation(t *testing.T) {
	store := NewMemoryExecutionStore()
	ctx := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	})
	execution := testCheckpoint("execution-1", "tenant-a")
	created, err := store.Create(ctx, execution)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.UpdatedAt.IsZero() {
		t.Fatalf("unexpected created checkpoint: %+v", created)
	}
	created.Input["invoice"] = "mutated"
	loaded, ok, err := store.Get(ctx, execution.ID)
	if err != nil || !ok {
		t.Fatalf("get checkpoint: ok=%t err=%v", ok, err)
	}
	if loaded.Input["invoice"] != "INV-1" {
		t.Fatalf("stored input was mutated: %+v", loaded.Input)
	}

	stale := loaded
	loaded.Status = ExecutionCompleted
	loaded.NextStep = 1
	loaded.Steps[0].Status = StepCompleted
	loaded.CompletedAt = time.Now().UTC()
	updated, err := store.Update(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("revision = %d, want 2", updated.Revision)
	}
	if _, err := store.Update(ctx, stale); !errors.Is(err, &core.SkawldError{Kind: core.ErrorConflict}) {
		t.Fatalf("stale update error = %v, want conflict", err)
	}
	updated.Status = ExecutionRunning
	updated.CompletedAt = time.Time{}
	if _, err := store.Update(ctx, updated); !errors.Is(
		err, &core.SkawldError{Kind: core.ErrorConflict},
	) {
		t.Fatalf("terminal execution mutation error = %v, want conflict", err)
	}

	other := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-b"})
	if _, _, err := store.Get(other, execution.ID); !errors.Is(
		err, &core.SkawldError{Kind: core.ErrorPermissionDenied},
	) {
		t.Fatalf("cross-tenant get error = %v, want permission denied", err)
	}
	if listed, err := store.List(other, ExecutionFilter{}); err != nil || len(listed) != 0 {
		t.Fatalf("cross-tenant list leaked checkpoints: %+v err=%v", listed, err)
	}
}

func TestMemoryExecutionStoreListsNewestWithFiltersAndLimit(t *testing.T) {
	store := NewMemoryExecutionStore()
	now := time.Unix(100, 0).UTC()
	store.now = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a"})
	first := testCheckpoint("first", "tenant-a")
	second := testCheckpoint("second", "tenant-a")
	second.WorkflowID = "other"
	if _, err := store.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(ctx, ExecutionFilter{Status: ExecutionRunning, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != second.ID {
		t.Fatalf("unexpected filtered checkpoints: %+v", listed)
	}
	listed, err = store.List(ctx, ExecutionFilter{WorkflowID: first.WorkflowID})
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("workflow filter: %+v err=%v", listed, err)
	}
}

func testCheckpoint(id, tenantID string) Execution {
	return Execution{
		ID: id, WorkflowID: "invoice", WorkflowVersion: 1,
		Principal: core.Principal{TenantID: tenantID, ActorID: "operator"},
		Status:    ExecutionRunning,
		Input:     map[string]interface{}{"invoice": "INV-1"},
		State:     map[string]interface{}{},
		Steps: []StepExecution{{
			StepID: "lookup", Status: StepRunning,
		}},
		StartedAt: time.Now().UTC(),
	}
}
