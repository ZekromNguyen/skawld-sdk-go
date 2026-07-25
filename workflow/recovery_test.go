package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestExecutorRecoversConfirmedToolCompletionWithoutReexecution(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"post": {
				Risk: core.RiskHigh, SideEffect: core.SideEffectNonIdempotent,
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
	executions := NewMemoryExecutionStore()
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Executions: executions, Policy: allowPolicy{},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	version := publishedVersion(Step{
		ID: "post", Kind: StepTool, Tool: &ToolCall{Name: "post"},
	})
	checkpoint := Execution{
		ID: "uncertain-1", WorkflowID: version.Workflow.ID,
		WorkflowVersion: version.Version, Principal: principal,
		Status: ExecutionRunning, Input: map[string]interface{}{},
		State: map[string]interface{}{}, Steps: []StepExecution{{
			StepID: "post", Status: StepRunning, Attempts: 1,
		}},
		StartedAt: time.Now().UTC(),
	}
	checkpoint, err = executions.Create(ctx, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := executor.Recover(
		ctx, version, checkpoint, RecoveryRequest{
			Decision: RecoveryConfirmedCompleted, StepID: "post",
			Output: map[string]interface{}{"id": "external-1"},
			Reason: "verified external transaction by idempotency record",
		}, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != ExecutionCompleted || len(runner.calls) != 0 {
		t.Fatalf("unexpected recovered execution: %+v calls=%v", recovered, runner.calls)
	}
}
