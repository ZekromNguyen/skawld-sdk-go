package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
)

func TestMemoryExecutionLeaseFencesStaleWorkers(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := NewMemoryExecutionStore()
	store.now = func() time.Time { return now }
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := store.Create(ctx, Execution{
		ID: "execution-1", WorkflowID: "workflow-1", WorkflowVersion: 1,
		Principal: principal, Status: ExecutionRunning,
		Input: map[string]interface{}{}, Context: map[string]interface{}{},
		State:     map[string]interface{}{},
		Steps:     []StepExecution{{StepID: "step-1", Status: StepPending}},
		StartedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, acquired, err := store.ClaimExecution(
		ctx, execution.ID, "worker-a", time.Second,
	)
	if err != nil || !acquired {
		t.Fatalf("first claim=%+v acquired=%v err=%v", first, acquired, err)
	}
	if _, acquired, err := store.ClaimExecution(
		ctx, execution.ID, "worker-b", time.Second,
	); err != nil || acquired {
		t.Fatalf("competing claim acquired=%v err=%v", acquired, err)
	}
	execution.Steps[0].Status = StepRunning
	if _, err := store.Update(ctx, execution); err == nil {
		t.Fatal("unclaimed update was accepted while lease was active")
	}
	now = now.Add(2 * time.Second)
	if _, err := store.Update(
		WithExecutionClaim(ctx, first), execution,
	); err == nil {
		t.Fatal("expired worker updated execution before takeover")
	}
	if err := store.ReleaseExecution(ctx, first); err == nil {
		t.Fatal("expired worker released its lease")
	}
	second, acquired, err := store.ClaimExecution(
		ctx, execution.ID, "worker-b", time.Second,
	)
	if err != nil || !acquired || second.Token <= first.Token {
		t.Fatalf("takeover=%+v acquired=%v err=%v", second, acquired, err)
	}
	if _, err := store.Update(
		WithExecutionClaim(ctx, first), execution,
	); err == nil {
		t.Fatal("stale fenced worker updated execution")
	}
	saved, err := store.Update(
		WithExecutionClaim(ctx, second), execution,
	)
	if err != nil || saved.Revision != 2 {
		t.Fatalf("claimed update=%+v err=%v", saved, err)
	}
	if err := store.ReleaseExecution(ctx, first); err == nil {
		t.Fatal("stale worker released current lease")
	}
	if err := store.ReleaseExecution(ctx, second); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorRequiresLeasedStoreWhenConfigured(t *testing.T) {
	_, err := NewExecutor(ExecutorOptions{
		Tools:      &fakeRunner{},
		Executions: nonLeasedExecutionStore{},
		WorkerID:   "worker-a", RequireExecutionLease: true,
	})
	if err == nil {
		t.Fatal("executor accepted a non-leased store")
	}
}

func TestExecutorCheckpointsUnderExecutionLease(t *testing.T) {
	store := NewMemoryExecutionStore()
	approvals := policy.NewMemoryApprovalStore()
	executor, err := NewExecutor(ExecutorOptions{
		Tools: &fakeRunner{}, Executions: store, Approvals: approvals,
		WorkerID: "worker-a", ExecutionLeaseDuration: time.Second,
		RequireExecutionLease: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "approval", Kind: StepApproval,
		Approval: &ApprovalSpec{
			Summary: "approve operation", Risk: core.RiskHigh,
		},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(ctx, version, nil, nil, principal)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionAwaitingApproval ||
		execution.Revision < 2 {
		t.Fatalf("leased execution=%+v", execution)
	}
	if _, acquired, err := store.ClaimExecution(
		ctx, execution.ID, "worker-b", time.Second,
	); err != nil || !acquired {
		t.Fatalf("executor did not release paused execution: acquired=%v err=%v", acquired, err)
	}
}

type nonLeasedExecutionStore struct{}

func (nonLeasedExecutionStore) Create(context.Context, Execution) (Execution, error) {
	return Execution{}, errors.New("unused")
}
func (nonLeasedExecutionStore) Update(context.Context, Execution) (Execution, error) {
	return Execution{}, errors.New("unused")
}
func (nonLeasedExecutionStore) Get(context.Context, string) (Execution, bool, error) {
	return Execution{}, false, errors.New("unused")
}
func (nonLeasedExecutionStore) List(context.Context, ExecutionFilter) ([]Execution, error) {
	return nil, errors.New("unused")
}
