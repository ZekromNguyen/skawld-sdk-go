package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
)

func TestExplicitCancellationClosesPendingApproval(t *testing.T) {
	approvals := policy.NewMemoryApprovalStore()
	executions := NewMemoryExecutionStore()
	audits := &audit.MemoryStore{}
	executor, err := NewExecutor(ExecutorOptions{
		Tools: &fakeRunner{}, Approvals: approvals,
		Executions: executions, Audit: audits,
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "approve", Kind: StepApproval,
		Approval: &ApprovalSpec{
			Summary: "approve payment", Risk: core.RiskHigh,
		},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(ctx, version, nil, nil, principal)
	if err != nil || execution.Status != ExecutionAwaitingApproval {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	approvalID := execution.PendingApprovalID
	canceled, err := executor.Cancel(
		ctx, execution, principal, "operator canceled duplicate task",
	)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != ExecutionCanceled ||
		canceled.Steps[0].Status != StepCanceled ||
		canceled.PendingApprovalID != "" {
		t.Fatalf("canceled execution=%+v", canceled)
	}
	approval, ok, err := approvals.Get(ctx, approvalID)
	if err != nil || !ok || approval.Status != policy.ApprovalCanceled {
		t.Fatalf("approval=%+v ok=%v err=%v", approval, ok, err)
	}
}

func TestWorkflowDeadlineExpiresAwaitingExecution(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	approvals := policy.NewMemoryApprovalStoreWithClock(
		func() time.Time { return now },
	)
	executions := NewMemoryExecutionStore()
	executions.now = func() time.Time { return now }
	executor, err := NewExecutor(ExecutorOptions{
		Tools: &fakeRunner{}, Approvals: approvals,
		Executions: executions, ExecutionTimeout: time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "approve", Kind: StepApproval,
		Approval: &ApprovalSpec{
			Summary: "approve payment", Risk: core.RiskHigh,
		},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(ctx, version, nil, nil, principal)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	expired, err := executor.Resume(ctx, version, execution)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status != ExecutionCanceled ||
		expired.Error == nil || expired.Error.Kind != core.ErrorTimeout {
		t.Fatalf("deadline execution=%+v", expired)
	}
}
