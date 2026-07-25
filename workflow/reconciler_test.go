package workflow

import (
	"context"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type fixtureReconciler struct {
	result ToolReconciliationResult
	calls  []ToolReconciliationRequest
}

func (r *fixtureReconciler) ReconcileTool(
	_ context.Context,
	request ToolReconciliationRequest,
) (ToolReconciliationResult, error) {
	r.calls = append(r.calls, request)
	return r.result, nil
}

func TestExecutorReconcilesAuthoritativeCompletionWithoutReplay(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"post": {
				Risk:       core.RiskLow,
				SideEffect: core.SideEffectNonIdempotent,
				OutputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string"},
					},
					"required": []interface{}{"id"},
				},
			},
		},
	}
	reconciler := &fixtureReconciler{
		result: ToolReconciliationResult{
			Outcome:      ReconciliationCompleted,
			Output:       map[string]interface{}{"id": "external-1"},
			EvidenceCode: "ledger.transaction_found",
			Reason:       "authoritative ledger query found the transaction",
		},
	}
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Policy: allowPolicy{},
		Reconciler: reconciler,
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	version := publishedVersion(Step{
		ID: "post", Kind: StepTool, Tool: &ToolCall{Name: "post"},
	})
	checkpoint := Execution{
		ID: "execution-1", WorkflowID: version.Workflow.ID,
		WorkflowVersion: version.Version, Principal: principal,
		Status: ExecutionRecoveryRequired,
		Input:  map[string]interface{}{}, State: map[string]interface{}{},
		Steps: []StepExecution{{
			StepID: "post", Status: StepRecoveryRequired,
			Attempts: 1, Input: map[string]interface{}{"amount": 10},
		}},
	}
	execution, err := executor.ReconcileRecovery(
		ctx, version, checkpoint, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionCompleted ||
		len(runner.calls) != 0 || len(reconciler.calls) != 1 {
		t.Fatalf(
			"reconciliation replayed the tool: execution=%+v calls=%v",
			execution, runner.calls,
		)
	}
}

func TestExecutorKeepsUnknownReconciliationRecoverable(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"post": {
				Risk:       core.RiskLow,
				SideEffect: core.SideEffectNonIdempotent,
			},
		},
	}
	reconciler := &fixtureReconciler{
		result: ToolReconciliationResult{
			Outcome:      ReconciliationUnknown,
			EvidenceCode: "ledger.timeout",
			Reason:       "authoritative ledger was unavailable",
		},
	}
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Policy: allowPolicy{},
		Reconciler: reconciler,
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	version := publishedVersion(Step{
		ID: "post", Kind: StepTool, Tool: &ToolCall{Name: "post"},
	})
	checkpoint := Execution{
		ID: "execution-1", WorkflowID: version.Workflow.ID,
		WorkflowVersion: version.Version, Principal: principal,
		Status: ExecutionRecoveryRequired,
		Input:  map[string]interface{}{}, State: map[string]interface{}{},
		Steps: []StepExecution{{
			StepID: "post", Status: StepRecoveryRequired, Attempts: 1,
		}},
	}
	execution, err := executor.ReconcileRecovery(
		ctx, version, checkpoint, principal,
	)
	if err == nil || execution.Status != ExecutionRecoveryRequired ||
		len(runner.calls) != 0 {
		t.Fatalf("unknown outcome changed execution: %+v err=%v", execution, err)
	}
}
