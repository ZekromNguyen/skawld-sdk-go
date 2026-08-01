package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
)

type fixedVersionResolver struct {
	version Version
}

func (r fixedVersionResolver) Get(
	ctx context.Context,
	workflowID string,
	version int,
) (Version, bool, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, false, err
	}
	if r.version.Workflow.ID != workflowID ||
		r.version.Version != version {
		return Version{}, false, nil
	}
	return r.version, true, nil
}

func TestCoordinatorMovesInterruptedSideEffectToRecovery(t *testing.T) {
	initiator := core.Principal{
		TenantID: "tenant-a", ActorID: "operator-a",
	}
	worker := core.Principal{
		TenantID: "tenant-a", ActorID: "worker-a",
		Roles: []string{"workflow.worker"},
	}
	initiatorCtx := core.WithPrincipal(context.Background(), initiator)
	store := NewMemoryExecutionStore()
	started := time.Now().UTC()
	checkpoint, err := store.Create(initiatorCtx, Execution{
		ID: "execution-1", WorkflowID: "workflow-1", WorkflowVersion: 1,
		Principal: initiator, Status: ExecutionRunning,
		Input: map[string]interface{}{}, State: map[string]interface{}{},
		Steps: []StepExecution{{
			StepID: "write", Status: StepRunning, Attempts: 1,
		}},
		StartedAt: started,
	})
	if err != nil {
		t.Fatal(err)
	}
	version := Version{
		SchemaVersion: SchemaVersion,
		Workflow: Workflow{
			ID: "workflow-1", TenantID: initiator.TenantID,
			Name: "write",
		},
		Version: 1, Status: VersionPublished, CreatedAt: started,
		Steps: []Step{{
			ID: "write", Kind: StepTool,
			Tool: &ToolCall{Name: "erp.update"},
		}},
	}
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"erp.update": {
				Risk:        core.RiskHigh,
				SideEffect:  core.SideEffectNonIdempotent,
				Idempotency: core.IdempotencyUnsupported,
				Timeout:     time.Second,
			},
		},
	}
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Policy: policy.RiskPolicy{},
		Executions: store, WorkerID: "worker-a",
		ExecutionLeaseDuration: time.Second,
		RequireExecutionLease:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Executor: executor, Executions: store,
		Versions: fixedVersionResolver{version: version},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := coordinator.RunOnce(
		core.WithPrincipal(context.Background(), worker),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.RecoveryRequired != 1 || len(runner.calls) != 0 {
		t.Fatalf("unexpected coordination report %+v calls=%v", report, runner.calls)
	}
	health := coordinator.Health()
	if !health.Ready || health.ConsecutiveFailures != 0 ||
		health.LastReport.RecoveryRequired != 1 ||
		health.WorkerID != "worker-a" ||
		!health.Healthy(time.Now().UTC(), time.Minute) {
		t.Fatalf("coordination health was not recorded: %+v", health)
	}
	stored, ok, err := store.Get(initiatorCtx, checkpoint.ID)
	if err != nil || !ok {
		t.Fatalf("load checkpoint: ok=%v err=%v", ok, err)
	}
	if stored.Status != ExecutionRecoveryRequired ||
		stored.Steps[0].Status != StepRecoveryRequired {
		t.Fatalf("interrupted side effect was not fenced: %+v", stored)
	}
}

func TestCoordinatorRequiresWorkerRole(t *testing.T) {
	store := NewMemoryExecutionStore()
	executor, err := NewExecutor(ExecutorOptions{
		Tools: &fakeRunner{}, Executions: store,
		WorkerID: "worker-a", ExecutionLeaseDuration: time.Second,
		RequireExecutionLease: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Executor: executor, Executions: store,
		Versions: fixedVersionResolver{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.RunOnce(core.WithPrincipal(
		context.Background(),
		core.Principal{TenantID: "tenant-a", ActorID: "actor-a"},
	))
	if err == nil {
		t.Fatal("expected worker role rejection")
	}
	health := coordinator.Health()
	if health.Ready || health.ConsecutiveFailures != 1 ||
		health.LastAttemptAt.IsZero() {
		t.Fatalf("coordination failure health was not recorded: %+v", health)
	}
}

func TestCoordinatorSkipsLiveLeasesBeforeApplyingBatchLimit(
	t *testing.T,
) {
	initiator := core.Principal{
		TenantID: "tenant-a", ActorID: "operator-a",
	}
	worker := core.Principal{
		TenantID: "tenant-a", ActorID: "worker-a",
		Roles: []string{"workflow.worker"},
	}
	initiatorCtx := core.WithPrincipal(context.Background(), initiator)
	store := NewMemoryExecutionStore()
	started := time.Now().UTC()
	for _, executionID := range []string{
		"execution-live-lease", "execution-ready",
	} {
		if _, err := store.Create(initiatorCtx, Execution{
			ID: executionID, WorkflowID: "workflow-1",
			WorkflowVersion: 1, Principal: initiator,
			Status: ExecutionRunning,
			Input:  map[string]interface{}{}, State: map[string]interface{}{},
			Steps: []StepExecution{{
				StepID: "write", Status: StepRunning, Attempts: 1,
			}},
			StartedAt: started,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, acquired, err := store.ClaimExecution(
		core.WithPrincipal(
			context.Background(),
			core.Principal{
				TenantID: "tenant-a", ActorID: "other-worker",
			},
		),
		"execution-live-lease", "other-worker", time.Minute,
	); err != nil || !acquired {
		t.Fatalf("create live lease: acquired=%v err=%v", acquired, err)
	}
	version := Version{
		SchemaVersion: SchemaVersion,
		Workflow: Workflow{
			ID: "workflow-1", TenantID: initiator.TenantID,
			Name: "write",
		},
		Version: 1, Status: VersionPublished, CreatedAt: started,
		Steps: []Step{{
			ID: "write", Kind: StepTool,
			Tool: &ToolCall{Name: "erp.update"},
		}},
	}
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"erp.update": {
				Risk:        core.RiskHigh,
				SideEffect:  core.SideEffectNonIdempotent,
				Idempotency: core.IdempotencyUnsupported,
				Timeout:     time.Second,
			},
		},
	}
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Policy: policy.RiskPolicy{},
		Executions: store, WorkerID: "worker-a",
		ExecutionLeaseDuration: time.Second,
		RequireExecutionLease:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Executor: executor, Executions: store,
		Versions: fixedVersionResolver{version: version},
		MaxBatch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := coordinator.RunOnce(
		core.WithPrincipal(context.Background(), worker),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 1 || report.RecoveryRequired != 1 {
		t.Fatalf("unexpected coordination report: %+v", report)
	}
	ready, ok, err := store.Get(initiatorCtx, "execution-ready")
	if err != nil || !ok {
		t.Fatalf("load ready execution: ok=%v err=%v", ok, err)
	}
	if ready.Status != ExecutionRecoveryRequired {
		t.Fatalf("ready execution was starved: %+v", ready)
	}
	live, ok, err := store.Get(
		initiatorCtx, "execution-live-lease",
	)
	if err != nil || !ok {
		t.Fatalf("load live execution: ok=%v err=%v", ok, err)
	}
	if live.Status != ExecutionRunning {
		t.Fatalf("live-leased execution was adopted: %+v", live)
	}
}
