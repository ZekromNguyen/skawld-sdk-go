package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
)

type durableExecutionTestStore struct {
	*MemoryExecutionStore
	outbox audit.Outbox
}

func (durableExecutionTestStore) Durable() bool   { return true }
func (durableExecutionTestStore) Protected() bool { return true }

func (s durableExecutionTestStore) AtomicWith(
	outbox audit.Outbox,
) bool {
	return s.outbox != nil && s.outbox == outbox
}

func (s durableExecutionTestStore) CreateWithEvents(
	ctx context.Context,
	execution Execution,
	events []audit.Event,
) (Execution, error) {
	for _, event := range events {
		if err := s.outbox.Enqueue(ctx, event); err != nil {
			return Execution{}, err
		}
	}
	return s.Create(ctx, execution)
}

func (s durableExecutionTestStore) UpdateWithEvents(
	ctx context.Context,
	execution Execution,
	events []audit.Event,
) (Execution, error) {
	for _, event := range events {
		if err := s.outbox.Enqueue(ctx, event); err != nil {
			return Execution{}, err
		}
	}
	return s.Update(ctx, execution)
}

type durableApprovalTestStore struct {
	*policy.MemoryApprovalStore
}

func (durableApprovalTestStore) Durable() bool   { return true }
func (durableApprovalTestStore) Protected() bool { return true }

type durableWorkflowOutbox struct {
	*audit.MemoryOutbox
}

func (durableWorkflowOutbox) Durable() bool   { return true }
func (durableWorkflowOutbox) Protected() bool { return true }

func TestProductionExecutorRejectsMemoryPersistence(t *testing.T) {
	authorization, err := policy.NewRolePolicy(
		policy.RolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"support": {"customer.read"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewProductionExecutor(ExecutorOptions{
		Tools: &fakeRunner{}, Policy: authorization,
		Approvals:   policy.NewMemoryApprovalStore(),
		AuditOutbox: audit.NewMemoryOutbox(),
		Executions:  NewMemoryExecutionStore(),
		Reconciler:  NewReconcilerRegistry(),
		ApprovalTTL: time.Minute, ExecutionTimeout: time.Minute,
		WorkerID: "worker-a", ExecutionLeaseDuration: time.Second,
		RequireExecutionLease: true,
		Production: &ExecutorProductionOptions{Limits: ExecutorLimits{
			MaxSteps: 10, MaxToolOutputBytes: 1024,
			MaxCheckpointBytes: 8192,
		}},
	})
	if err == nil {
		t.Fatal("expected non-durable workflow persistence rejection")
	}
}

func TestProductionExecutorAcceptsExplicitDurableProfile(t *testing.T) {
	outbox := &durableWorkflowOutbox{
		MemoryOutbox: audit.NewMemoryOutbox(),
	}
	executions := durableExecutionTestStore{
		MemoryExecutionStore: NewMemoryExecutionStore(),
		outbox:               outbox,
	}
	authorization, err := policy.NewRolePolicy(
		policy.RolePolicyOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewProductionExecutor(ExecutorOptions{
		Tools: &fakeRunner{}, Policy: authorization,
		Approvals: durableApprovalTestStore{
			MemoryApprovalStore: policy.NewMemoryApprovalStore(),
		},
		AuditOutbox: outbox,
		Executions:  executions,
		Reconciler:  NewReconcilerRegistry(),
		ApprovalTTL: time.Minute, ExecutionTimeout: time.Minute,
		WorkerID: "worker-a", ExecutionLeaseDuration: time.Second,
		RequireExecutionLease: true,
		Production: &ExecutorProductionOptions{Limits: ExecutorLimits{
			MaxSteps: 10, MaxToolOutputBytes: 1024,
			MaxCheckpointBytes: 8192,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !executor.production || executor.limits.MaxSteps != 10 {
		t.Fatalf("production limits were not installed: %+v", executor)
	}
}

func TestProductionExecutorRejectsOversizedToolOutput(t *testing.T) {
	outbox := &durableWorkflowOutbox{
		MemoryOutbox: audit.NewMemoryOutbox(),
	}
	executions := durableExecutionTestStore{
		MemoryExecutionStore: NewMemoryExecutionStore(),
		outbox:               outbox,
	}
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"lookup": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				Idempotency: core.IdempotencyNotApplicable,
				Timeout:     time.Second,
				Permissions: []string{
					"customer.read",
				},
				OutputSchema: map[string]interface{}{
					"type": "string",
				},
			},
		},
		outputs: map[string]interface{}{
			"lookup": strings.Repeat("x", 32),
		},
	}
	authorization, err := policy.NewRolePolicy(
		policy.RolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"support": {"customer.read"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewProductionExecutor(ExecutorOptions{
		Tools: runner, Policy: authorization,
		Approvals: durableApprovalTestStore{
			MemoryApprovalStore: policy.NewMemoryApprovalStore(),
		},
		AuditOutbox: outbox,
		Executions:  executions,
		Reconciler:  NewReconcilerRegistry(),
		ApprovalTTL: time.Minute, ExecutionTimeout: time.Minute,
		WorkerID: "worker-a", ExecutionLeaseDuration: time.Second,
		RequireExecutionLease: true,
		Production: &ExecutorProductionOptions{Limits: ExecutorLimits{
			MaxSteps: 10, MaxToolOutputBytes: 8,
			MaxCheckpointBytes: 8192,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
		Roles: []string{"support"},
	}
	version := publishedVersion(Step{
		ID: "lookup", Kind: StepTool,
		Tool: &ToolCall{Name: "lookup"},
	})
	version.Workflow.TenantID = principal.TenantID
	execution, err := executor.Execute(
		core.WithPrincipal(context.Background(), principal),
		version, map[string]interface{}{}, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionFailed ||
		execution.Error == nil ||
		execution.Error.Kind != core.ErrorValidation {
		t.Fatalf("oversized output was not rejected: %+v", execution)
	}
}

func TestProductionExecutorRequiresRecoveryPathForUnsafeTool(t *testing.T) {
	outbox := &durableWorkflowOutbox{
		MemoryOutbox: audit.NewMemoryOutbox(),
	}
	executions := durableExecutionTestStore{
		MemoryExecutionStore: NewMemoryExecutionStore(),
		outbox:               outbox,
	}
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"transfer": {
				Risk:        core.RiskHigh,
				SideEffect:  core.SideEffectNonIdempotent,
				Idempotency: core.IdempotencyUnsupported,
				Timeout:     time.Second,
				Permissions: []string{"payment.create"},
				OutputSchema: map[string]interface{}{
					"type": "object",
				},
			},
		},
	}
	authorization, err := policy.NewRolePolicy(
		policy.RolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"operator": {"payment.create"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewProductionExecutor(ExecutorOptions{
		Tools: runner, Policy: authorization,
		Approvals: durableApprovalTestStore{
			MemoryApprovalStore: policy.NewMemoryApprovalStore(),
		},
		AuditOutbox: outbox,
		Executions:  executions,
		Reconciler:  NewReconcilerRegistry(),
		ApprovalTTL: time.Minute, ExecutionTimeout: time.Minute,
		WorkerID: "worker-a", ExecutionLeaseDuration: time.Second,
		RequireExecutionLease: true,
		Production: &ExecutorProductionOptions{Limits: ExecutorLimits{
			MaxSteps: 10, MaxToolOutputBytes: 1024,
			MaxCheckpointBytes: 8192,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
		Roles: []string{"operator"},
	}
	version := publishedVersion(Step{
		ID: "transfer", Kind: StepTool,
		Tool: &ToolCall{Name: "transfer"},
	})
	version.Workflow.TenantID = principal.TenantID

	_, err = executor.Execute(
		core.WithPrincipal(context.Background(), principal),
		version, map[string]interface{}{}, nil, principal,
	)
	if err == nil || !strings.Contains(err.Error(), "requires an authoritative reconciler") {
		t.Fatalf("unsafe tool was not rejected during preflight: %v", err)
	}
}
