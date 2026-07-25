package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
)

type fakeRunner struct {
	descriptors   map[string]core.ToolDescriptor
	outputs       map[string]interface{}
	errs          map[string]error
	calls         []string
	catalogDigest string
}

type alwaysFailAudit struct{}

func (alwaysFailAudit) Append(context.Context, audit.Event) error {
	return errors.New("audit backend unavailable")
}

type selectiveFailOutbox struct {
	delegate *audit.MemoryOutbox
	failType audit.EventType
}

func (s selectiveFailOutbox) Enqueue(
	ctx context.Context,
	event audit.Event,
) error {
	if event.Type == s.failType {
		return errors.New("audit outbox unavailable")
	}
	return s.delegate.Enqueue(ctx, event)
}

func (s selectiveFailOutbox) Pending(
	ctx context.Context,
	limit int,
) ([]audit.Delivery, error) {
	return s.delegate.Pending(ctx, limit)
}

func (s selectiveFailOutbox) MarkAttempt(
	ctx context.Context,
	eventID string,
	message string,
) error {
	return s.delegate.MarkAttempt(ctx, eventID, message)
}

func (s selectiveFailOutbox) MarkDelivered(
	ctx context.Context,
	eventID string,
) error {
	return s.delegate.MarkDelivered(ctx, eventID)
}

func (r *fakeRunner) ToolCatalogFingerprint(context.Context, []string) (string, error) {
	return r.catalogDigest, nil
}

func (r *fakeRunner) Describe(ctx context.Context, name string) (core.ToolDescriptor, bool, error) {
	descriptor, ok := r.descriptors[name]
	return descriptor, ok, ctx.Err()
}

func (r *fakeRunner) Execute(ctx context.Context, name string, input map[string]interface{}, idempotencyKey string) (ToolResult, error) {
	r.calls = append(r.calls, name)
	if err := r.errs[name]; err != nil {
		return ToolResult{Retryable: true}, err
	}
	return ToolResult{Output: r.outputs[name]}, nil
}

func publishedVersion(steps ...Step) Version {
	return Version{
		SchemaVersion: SchemaVersion,
		Workflow:      Workflow{ID: "invoice-reconciliation", Name: "Invoice reconciliation"},
		Version:       1,
		Status:        VersionPublished,
		CreatedAt:     time.Now(),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"invoice": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":    map[string]interface{}{"type": "string"},
						"po_id": map[string]interface{}{"type": "string"},
						"total": map[string]interface{}{"type": "number"},
					},
				},
			},
		},
		Steps: steps,
	}
}

func TestExecutorRunsDeterministicReferencesAndValidation(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"lookup_po": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				Idempotency: core.IdempotencyNotApplicable,
				OutputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"total": map[string]interface{}{"type": "number"},
					},
				},
			},
		},
		outputs: map[string]interface{}{"lookup_po": map[string]interface{}{"total": 500.0}},
	}
	log := &audit.MemoryStore{}
	executor, err := NewExecutor(ExecutorOptions{Tools: runner, Audit: log})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(
		Step{
			ID: "lookup", Kind: StepTool,
			Tool: &ToolCall{Name: "lookup_po", Arguments: map[string]Value{
				"id": {Ref: "input.invoice.po_id"},
			}},
		},
		Step{
			ID: "totals_match", Kind: StepValidation, DependsOn: []string{"lookup"},
			Validation: &Validation{Condition: Condition{
				Left: Value{Ref: "steps.lookup.output.total"}, Operator: OpEqual,
				Right: Value{Ref: "input.invoice.total"},
			}},
		},
	)
	principal := core.Principal{TenantID: "tenant-a", ActorID: "accountant-1"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(ctx, version, map[string]interface{}{
		"invoice": map[string]interface{}{"po_id": "PO-1", "total": 500.0},
	}, nil, principal)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionCompleted || len(runner.calls) != 1 {
		t.Fatalf("unexpected execution status=%s calls=%v error=%+v", execution.Status, runner.calls, execution.Error)
	}
	events, err := log.List(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 6 || events[0].TenantID != "tenant-a" || events[0].WorkflowVersion != 1 {
		t.Fatalf("incomplete audit trail: %+v", events)
	}
}

func TestExecutorReferencePreflightFailsBeforeAnyToolSideEffect(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"lookup_po": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				Idempotency: core.IdempotencyNotApplicable,
			},
		},
	}
	executor, err := NewExecutor(ExecutorOptions{Tools: runner})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(
		Step{
			ID: "lookup", Kind: StepTool,
			Tool: &ToolCall{Name: "lookup_po", Arguments: map[string]Value{
				"id": {Ref: "input.invoice.po_id"},
			}},
		},
		Step{
			ID: "check", Kind: StepValidation,
			Validation: &Validation{Condition: Condition{
				Left: Value{Ref: "steps.lookup.output.total"}, Operator: OpExists,
			}},
		},
	)
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	_, err = executor.Execute(
		core.WithPrincipal(context.Background(), principal), version,
		map[string]interface{}{"invoice": map[string]interface{}{"po_id": "PO-1"}},
		nil, principal,
	)
	if err == nil {
		t.Fatal("expected undeclared tool output reference to fail preflight")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("tool executed before reference validation: %v", runner.calls)
	}

	version = publishedVersion(Step{
		ID: "lookup", Kind: StepTool,
		Tool: &ToolCall{Name: "lookup_po", Arguments: map[string]Value{
			"id": {Ref: "input.invoice.po_id"},
		}},
	})
	_, err = executor.Execute(
		core.WithPrincipal(context.Background(), principal), version,
		map[string]interface{}{"invoice": map[string]interface{}{}},
		nil, principal,
	)
	if err == nil {
		t.Fatal("expected absent input reference to fail preflight")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("tool executed with missing trusted input: %v", runner.calls)
	}
}

func TestExecutorRejectsInvalidToolOutputSchemaBeforeExecution(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"lookup_po": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				OutputSchema: map[string]interface{}{
					"type": "executable",
				},
			},
		},
	}
	executor, err := NewExecutor(ExecutorOptions{Tools: runner})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "lookup", Kind: StepTool,
		Tool: &ToolCall{Name: "lookup_po"},
	})
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	_, err = executor.Execute(
		core.WithPrincipal(context.Background(), principal),
		version, map[string]interface{}{}, nil, principal,
	)
	if err == nil {
		t.Fatal("expected invalid output schema to fail preflight")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("tool executed with invalid output schema: %v", runner.calls)
	}
}

func TestExecutorRejectsToolCatalogDriftBeforeSideEffects(t *testing.T) {
	runner := &fakeRunner{
		catalogDigest: "current",
		descriptors: map[string]core.ToolDescriptor{
			"lookup_po": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				Idempotency: core.IdempotencyNotApplicable,
			},
		},
	}
	executor, err := NewExecutor(ExecutorOptions{Tools: runner})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "lookup", Kind: StepTool,
		Tool: &ToolCall{Name: "lookup_po"},
	})
	version.ToolCatalogDigest = "compiled"
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	_, err = executor.Execute(
		core.WithPrincipal(context.Background(), principal),
		version, map[string]interface{}{}, nil, principal,
	)
	if err == nil {
		t.Fatal("expected tool catalog drift to fail execution")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("tool executed despite catalog drift: %v", runner.calls)
	}
}

func TestExecutorPausesAndResumesHighRiskToolExactlyOnce(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"mark_reviewed": {
				Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
				Idempotency: core.IdempotencyRequired,
			},
		},
		outputs: map[string]interface{}{"mark_reviewed": map[string]interface{}{"status": "reviewed"}},
	}
	approvals := policy.NewMemoryApprovalStore()
	executor, err := NewExecutor(ExecutorOptions{Tools: runner, Approvals: approvals})
	if err != nil {
		t.Fatal(err)
	}
	key := Value{Ref: "input.invoice.id"}
	version := publishedVersion(Step{
		ID: "mark", Kind: StepTool,
		Tool: &ToolCall{
			Name: "mark_reviewed", Arguments: map[string]Value{"invoice_id": key},
			IdempotencyKey: &key, Reason: "invoice and purchase order matched",
		},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "accountant-1"}
	execution, err := executor.Execute(
		core.WithPrincipal(context.Background(), principal),
		version, map[string]interface{}{
			"invoice": map[string]interface{}{"id": "INV-1"},
		}, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionAwaitingApproval || execution.PendingApprovalID == "" || len(runner.calls) != 0 {
		t.Fatalf("tool ran before approval: status=%s calls=%v", execution.Status, runner.calls)
	}
	if _, err := approvals.Decide(
		core.WithPrincipal(context.Background(), principal),
		execution.PendingApprovalID, policy.ApprovalGranted, principal,
		"matched fixtures",
	); err != nil {
		t.Fatal(err)
	}
	execution, err = executor.Resume(
		core.WithPrincipal(context.Background(), principal),
		version, execution,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionCompleted || len(runner.calls) != 1 {
		t.Fatalf("expected one approved execution, status=%s calls=%v", execution.Status, runner.calls)
	}
}

func TestExecutorExpiresApprovalBeforeToolExecution(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"post": {
				Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
			},
		},
	}
	approvals := policy.NewMemoryApprovalStore()
	executions := NewMemoryExecutionStore()
	now := time.Now().UTC()
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Approvals: approvals, Executions: executions,
		ApprovalTTL: time.Nanosecond,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "post", Kind: StepTool, Tool: &ToolCall{Name: "post"},
	})
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(
		ctx, version, map[string]interface{}{}, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionAwaitingApproval {
		t.Fatalf("execution status = %q, want awaiting approval", execution.Status)
	}
	approvalID := execution.PendingApprovalID
	now = now.Add(time.Second)
	execution, err = executor.Resume(ctx, version, execution)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionFailed ||
		execution.Error == nil ||
		execution.Error.Kind != core.ErrorApproval ||
		len(runner.calls) != 0 {
		t.Fatalf("expired approval did not fail closed: %+v", execution)
	}
	approval, ok, err := approvals.Get(
		ctx, approvalID,
	)
	if err != nil || !ok || approval.Status != policy.ApprovalExpired {
		t.Fatalf(
			"expiration was not persisted: %+v ok=%t err=%v",
			approval, ok, err,
		)
	}
	durable, ok, err := executions.Get(ctx, execution.ID)
	if err != nil || !ok || durable.Status != ExecutionFailed ||
		durable.PendingApprovalID != "" {
		t.Fatalf(
			"terminal expiration checkpoint = %+v ok=%t err=%v",
			durable, ok, err,
		)
	}
}

func TestExecutorPersistsApprovalCheckpointAndResumesAfterRestart(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"mark_reviewed": {
				Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
				Idempotency: core.IdempotencyRequired,
			},
		},
		outputs: map[string]interface{}{
			"mark_reviewed": map[string]interface{}{"status": "reviewed"},
		},
	}
	approvals := policy.NewMemoryApprovalStore()
	executions := NewMemoryExecutionStore()
	firstExecutor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Approvals: approvals, Executions: executions,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := Value{Ref: "input.invoice.id"}
	version := publishedVersion(Step{
		ID: "mark", Kind: StepTool,
		Tool: &ToolCall{
			Name: "mark_reviewed", Arguments: map[string]Value{"invoice_id": key},
			IdempotencyKey: &key,
		},
	})
	version.Workflow.TenantID = "tenant-a"
	principal := core.Principal{TenantID: "tenant-a", ActorID: "accountant-1"}
	ctx := core.WithPrincipal(context.Background(), principal)
	checkpoint, err := firstExecutor.Execute(ctx, version, map[string]interface{}{
		"invoice": map[string]interface{}{"id": "INV-1"},
	}, nil, principal)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Status != ExecutionAwaitingApproval || checkpoint.Revision < 2 {
		t.Fatalf("unexpected durable approval checkpoint: %+v", checkpoint)
	}
	loaded, ok, err := executions.Get(ctx, checkpoint.ID)
	if err != nil || !ok || loaded.Revision != checkpoint.Revision ||
		loaded.PendingApprovalID != checkpoint.PendingApprovalID {
		t.Fatalf("load approval checkpoint: ok=%t execution=%+v err=%v", ok, loaded, err)
	}
	if _, err := approvals.Decide(
		ctx, loaded.PendingApprovalID, policy.ApprovalGranted, principal, "reviewed",
	); err != nil {
		t.Fatal(err)
	}

	restartedExecutor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Approvals: approvals, Executions: executions,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := restartedExecutor.Resume(ctx, version, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != ExecutionCompleted || len(runner.calls) != 1 {
		t.Fatalf("resume after restart: execution=%+v calls=%v", completed, runner.calls)
	}
	durable, ok, err := executions.Get(ctx, completed.ID)
	if err != nil || !ok || durable.Status != ExecutionCompleted ||
		durable.Revision != completed.Revision {
		t.Fatalf("load completed execution: ok=%t execution=%+v err=%v", ok, durable, err)
	}
}

func TestExecutorForbidsRetryForUnknownSideEffect(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"transfer": {Risk: core.RiskMedium, SideEffect: core.SideEffectUnknown, Idempotency: core.IdempotencyUnsupported},
		},
		errs: map[string]error{"transfer": errors.New("timeout")},
	}
	executor, err := NewExecutor(ExecutorOptions{
		Tools:  runner,
		Policy: allowPolicy{},
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "transfer", Kind: StepTool, Tool: &ToolCall{Name: "transfer"},
		Retry: RetryPolicy{MaxAttempts: 2},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	execution, err := executor.Execute(
		core.WithPrincipal(context.Background(), principal),
		version, nil, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionFailed || len(runner.calls) != 0 || execution.Error.Kind != core.ErrorValidation {
		t.Fatalf("unsafe retry was not rejected: execution=%+v calls=%v", execution, runner.calls)
	}
}

func TestExecutorRejectsPartialOrMismatchedIdentity(t *testing.T) {
	runner := &fakeRunner{}
	executor, err := NewExecutor(ExecutorOptions{Tools: runner})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "check", Kind: StepValidation,
		Validation: &Validation{Condition: Condition{
			Left: Value{Literal: true}, Operator: OpEqual,
			Right: Value{Literal: true},
		}},
	})
	if _, err := executor.Execute(
		context.Background(), version, nil, nil,
		core.Principal{TenantID: "tenant-a"},
	); err == nil {
		t.Fatal("expected partial identity to be rejected")
	}
	ctx := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	})
	if _, err := executor.Execute(
		ctx, version, nil, nil,
		core.Principal{TenantID: "tenant-a", ActorID: "actor-b"},
	); err == nil {
		t.Fatal("expected context identity mismatch to be rejected")
	}
}

func TestExecutorRejectsInvalidToolOutputBeforeDependentStep(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"lookup": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				OutputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"total": map[string]interface{}{"type": "number"},
					},
					"required": []interface{}{"total"},
				},
			},
		},
		outputs: map[string]interface{}{
			"lookup": map[string]interface{}{"total": "not-a-number"},
		},
	}
	executor, err := NewExecutor(ExecutorOptions{Tools: runner})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "lookup", Kind: StepTool, Tool: &ToolCall{Name: "lookup"},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
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
		t.Fatalf("invalid output execution = %+v", execution)
	}
}

func TestExecutorCompletesWhenAuditDeliveryFailsAfterDurableEnqueue(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"lookup": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				OutputSchema: map[string]interface{}{"type": "object"},
			},
		},
		outputs: map[string]interface{}{
			"lookup": map[string]interface{}{"ok": true},
		},
	}
	outbox := audit.NewMemoryOutbox()
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Audit: alwaysFailAudit{}, AuditOutbox: outbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "lookup", Kind: StepTool, Tool: &ToolCall{Name: "lookup"},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(
		ctx, version, map[string]interface{}{}, nil, principal,
	)
	if err != nil || execution.Status != ExecutionCompleted {
		t.Fatalf("execution = %+v err=%v", execution, err)
	}
	pending, err := outbox.Pending(ctx, 100)
	if err != nil || len(pending) == 0 {
		t.Fatalf("failed audit was not retained: %+v err=%v", pending, err)
	}
}

func TestExecutorRequiresRecoveryWhenSideEffectAuditEnqueueFails(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"post": {
				Risk: core.RiskLow, SideEffect: core.SideEffectIdempotent,
				OutputSchema: map[string]interface{}{"type": "object"},
			},
		},
		outputs: map[string]interface{}{
			"post": map[string]interface{}{"id": "external-123"},
		},
	}
	executions := NewMemoryExecutionStore()
	outbox := selectiveFailOutbox{
		delegate: audit.NewMemoryOutbox(),
		failType: audit.EventToolCompleted,
	}
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Policy: allowPolicy{}, Executions: executions,
		AuditOutbox: outbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "post", Kind: StepTool, Tool: &ToolCall{Name: "post"},
	})
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(
		ctx, version, map[string]interface{}{}, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionRecoveryRequired ||
		execution.Steps[0].Status != StepRecoveryRequired ||
		len(runner.calls) != 1 {
		t.Fatalf("unsafe audit failure result: %+v calls=%v", execution, runner.calls)
	}
	durable, ok, loadErr := executions.Get(ctx, execution.ID)
	if loadErr != nil || !ok ||
		durable.Status != ExecutionRecoveryRequired {
		t.Fatalf(
			"recovery checkpoint was not durable: %+v ok=%t err=%v",
			durable, ok, loadErr,
		)
	}
}

func TestExecutorRequiresRecoveryForInvalidSideEffectOutput(t *testing.T) {
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
		outputs: map[string]interface{}{
			"post": map[string]interface{}{"id": 123.0},
		},
	}
	executions := NewMemoryExecutionStore()
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Policy: allowPolicy{}, Executions: executions,
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "post", Kind: StepTool, Tool: &ToolCall{Name: "post"},
	})
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(
		ctx, version, map[string]interface{}{}, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionRecoveryRequired ||
		execution.Steps[0].Status != StepRecoveryRequired ||
		len(runner.calls) != 1 {
		t.Fatalf("unexpected uncertain execution: %+v", execution)
	}
	recovered, err := executor.Recover(
		ctx, version, execution, RecoveryRequest{
			Decision: RecoveryConfirmedCompleted, StepID: "post",
			Output: map[string]interface{}{"id": "external-123"},
			Reason: "verified the external record",
		}, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != ExecutionCompleted || len(runner.calls) != 1 {
		t.Fatalf(
			"recovery reran side effect: execution=%+v calls=%v",
			recovered, runner.calls,
		)
	}
}

func TestDefaultExecutorDeniesUndelegatedToolCapability(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"read_invoice": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				Permissions: []string{"invoice.read"},
			},
		},
	}
	executor, err := NewExecutor(ExecutorOptions{Tools: runner})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "read", Kind: StepTool,
		Tool: &ToolCall{Name: "read_invoice"},
	})
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
		Roles: []string{"accountant"},
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution, err := executor.Execute(
		ctx, version, map[string]interface{}{}, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionFailed ||
		execution.Error == nil ||
		execution.Error.Kind != core.ErrorPolicy ||
		len(runner.calls) != 0 {
		t.Fatalf("undelegated capability was not denied: %+v", execution)
	}
}

func TestExecutorUsesAuthenticatedContextRolesForAuthorization(t *testing.T) {
	runner := &fakeRunner{
		descriptors: map[string]core.ToolDescriptor{
			"read_invoice": {
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				Permissions: []string{"invoice.read"},
			},
		},
		outputs: map[string]interface{}{
			"read_invoice": map[string]interface{}{"id": "INV-1"},
		},
	}
	authorization, err := policy.NewRolePolicy(policy.RolePolicyOptions{
		RoleCapabilities: map[string][]string{
			"accountant": {"invoice.read"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(ExecutorOptions{
		Tools: runner, Policy: authorization,
	})
	if err != nil {
		t.Fatal(err)
	}
	version := publishedVersion(Step{
		ID: "read", Kind: StepTool,
		Tool: &ToolCall{Name: "read_invoice"},
	})
	argumentPrincipal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
		Roles: []string{"accountant"},
	}
	authenticatedPrincipal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
		Roles: []string{"viewer"},
	}
	execution, err := executor.Execute(
		core.WithPrincipal(context.Background(), authenticatedPrincipal),
		version, map[string]interface{}{}, nil, argumentPrincipal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionFailed ||
		execution.Error == nil ||
		execution.Error.Kind != core.ErrorPolicy ||
		len(runner.calls) != 0 {
		t.Fatalf("method argument roles bypassed context claims: %+v", execution)
	}
	if len(execution.Principal.Roles) != 1 ||
		execution.Principal.Roles[0] != "viewer" {
		t.Fatalf(
			"execution did not persist authenticated claims: %+v",
			execution.Principal,
		)
	}
}

type allowPolicy struct{}

func (allowPolicy) Evaluate(context.Context, policy.Action) (policy.Decision, error) {
	return policy.Decision{Kind: policy.Allow}, nil
}

func TestMemoryStorePublishesImmutableVersions(t *testing.T) {
	store := NewMemoryStore()
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a", ActorID: "reviewer"})
	version := publishedVersion(Step{ID: "check", Kind: StepValidation, Validation: &Validation{
		Condition: Condition{Left: Value{Literal: 1.0}, Operator: OpEqual, Right: Value{Literal: 1.0}},
	}})
	version.Status = VersionCandidate
	version.Workflow.TenantID = "tenant-a"
	saved, err := store.SaveCandidate(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	saved.Steps[0].ID = "mutated"
	published, err := store.Publish(ctx, version.Workflow.ID, 1, core.Principal{TenantID: "tenant-a", ActorID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if published.Steps[0].ID != "check" || published.Status != VersionPublished || published.PublishedBy != "reviewer" {
		t.Fatalf("unexpected published version: %+v", published)
	}
}
