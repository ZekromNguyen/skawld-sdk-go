package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func TestExecutionTransitionRollsBackCheckpointAndOutboxTogether(
	t *testing.T,
) {
	store, err := Open(
		context.Background(),
		filepath.Join(t.TempDir(), "transitions.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	transitions, ok := store.Executions().(workflow.ExecutionTransitionStore)
	if !ok || !transitions.AtomicWith(store.AuditOutbox()) {
		t.Fatal("SQLite stores do not expose an atomic transition domain")
	}
	other, err := Open(
		context.Background(),
		filepath.Join(t.TempDir(), "other-transitions.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if transitions.AtomicWith(other.AuditOutbox()) {
		t.Fatal("different SQLite stores claimed one transaction domain")
	}
	started := time.Now().UTC()
	execution := workflow.Execution{
		ID: "execution-create-rollback", WorkflowID: "workflow-1",
		WorkflowVersion: 1, Principal: principal,
		Status: workflow.ExecutionRunning,
		Input:  map[string]interface{}{}, Context: map[string]interface{}{},
		State: map[string]interface{}{},
		Steps: []workflow.StepExecution{{
			StepID: "step-1", Status: workflow.StepPending,
		}},
		StartedAt: started,
	}
	duplicate := audit.Event{
		ID: "duplicate-event", Type: audit.EventExecutionStarted,
		Timestamp: started, TenantID: principal.TenantID,
		ActorID: principal.ActorID, ExecutionID: execution.ID,
	}
	if _, err := transitions.CreateWithEvents(
		ctx, execution, []audit.Event{duplicate, duplicate},
	); err == nil {
		t.Fatal("duplicate outbox event did not fail the transition")
	}
	if _, exists, err := transitions.Get(
		ctx, execution.ID,
	); err != nil || exists {
		t.Fatalf(
			"failed create transition persisted execution: exists=%v err=%v",
			exists, err,
		)
	}
	pending, err := store.AuditOutbox().Pending(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf(
			"failed create transition persisted outbox rows: %+v err=%v",
			pending, err,
		)
	}

	execution.ID = "execution-update-rollback"
	created, err := transitions.CreateWithEvents(ctx, execution, nil)
	if err != nil {
		t.Fatal(err)
	}
	conflict := duplicate
	conflict.ID = "existing-event"
	conflict.ExecutionID = created.ID
	if err := store.AuditOutbox().Enqueue(ctx, conflict); err != nil {
		t.Fatal(err)
	}
	created.Steps[0].Status = workflow.StepRunning
	if _, err := transitions.UpdateWithEvents(
		ctx, created, []audit.Event{conflict},
	); err == nil {
		t.Fatal("duplicate outbox event did not fail update transition")
	}
	loaded, exists, err := transitions.Get(ctx, created.ID)
	if err != nil || !exists {
		t.Fatalf("load rolled-back execution: exists=%v err=%v", exists, err)
	}
	if loaded.Revision != created.Revision ||
		loaded.Steps[0].Status != workflow.StepPending {
		t.Fatalf("failed update transition changed checkpoint: %+v", loaded)
	}
}

func TestClaimReadyExecutionsFiltersLiveLeasesBeforeLimit(
	t *testing.T,
) {
	store, err := Open(
		context.Background(),
		filepath.Join(t.TempDir(), "ready-claims.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "worker",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	started := time.Now().UTC()
	for _, executionID := range []string{"live", "ready"} {
		if _, err := store.Executions().Create(
			ctx,
			workflow.Execution{
				ID: executionID, WorkflowID: "workflow-1",
				WorkflowVersion: 1, Principal: principal,
				Status: workflow.ExecutionRunning,
				Input:  map[string]interface{}{},
				State:  map[string]interface{}{},
				Steps: []workflow.StepExecution{{
					StepID: "step-1", Status: workflow.StepRunning,
				}},
				StartedAt: started,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	leases := store.LeasedExecutions()
	if _, acquired, err := leases.ClaimExecution(
		ctx, "live", "worker-a", time.Minute,
	); err != nil || !acquired {
		t.Fatalf("live lease: acquired=%v err=%v", acquired, err)
	}
	claimer, ok := store.Executions().(workflow.ReadyExecutionClaimer)
	if !ok {
		t.Fatal("SQLite execution store does not support ready claims")
	}
	claims, err := claimer.ClaimReadyExecutions(
		ctx, workflow.ReadyExecutionClaimRequest{
			Statuses: []workflow.ExecutionStatus{
				workflow.ExecutionRunning,
			},
			Owner: "worker-b", Duration: time.Minute, Limit: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Execution.ID != "ready" {
		t.Fatalf("ready claims = %+v, want only ready execution", claims)
	}
}
