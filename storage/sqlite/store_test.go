package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	sdkstorage "github.com/ZekromNguyen/skawld-sdk-go/storage"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func TestProtectedSQLiteDocumentsAreEncryptedAndReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected.db")
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	keys, err := sdkstorage.NewStaticKeyProvider(
		map[string]sdkstorage.EncryptionKey{
			principal.TenantID: {
				ID: "key-1", Bytes: bytes.Repeat([]byte{7}, 32),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := sdkstorage.NewAESGCMProtector(keys)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenWithOptions(ctx, path, Options{
		Protector: protector, RequireProtection: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	executions, ok := store.Executions().(workflow.ProtectedExecutionStore)
	if !ok || !executions.Protected() {
		t.Fatal("protected execution-store capability was not exposed")
	}
	approvals, ok := store.Approvals().(policy.ProtectedApprovalStore)
	if !ok || !approvals.Protected() {
		t.Fatal("protected approval-store capability was not exposed")
	}
	outbox, ok := store.AuditOutbox().(audit.ProtectedOutbox)
	if !ok || !outbox.Protected() {
		t.Fatal("protected audit-outbox capability was not exposed")
	}
	event := audit.Event{
		ID: "protected-event", Type: audit.EventExecutionEnded,
		Timestamp: time.Now().UTC(), TenantID: principal.TenantID,
		ActorID: principal.ActorID, Outcome: "sensitive-invoice",
	}
	if err := store.Audit().Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.AuditOutbox().Enqueue(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.AuditOutbox().Enqueue(ctx, event); err != nil {
		t.Fatalf("protected outbox retry was not idempotent: %v", err)
	}
	var raw []byte
	if err := store.db.QueryRowContext(
		ctx, `SELECT document_json FROM audit_events WHERE id = ?`,
		event.ID,
	).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(event.Outcome)) {
		t.Fatalf("stored document contains plaintext: %s", raw)
	}
	events, err := store.Audit().List(ctx, event.ExecutionID)
	if err != nil || len(events) != 1 ||
		events[0].Outcome != event.Outcome {
		t.Fatalf("protected audit read=%+v err=%v", events, err)
	}
	if err := keys.Rotate(principal.TenantID, sdkstorage.EncryptionKey{
		ID: "key-2", Bytes: bytes.Repeat([]byte{8}, 32),
	}); err != nil {
		t.Fatal(err)
	}
	rotated, err := store.RotateProtectedDocuments(ctx)
	if err != nil || rotated != 2 {
		t.Fatalf("rotated=%d err=%v", rotated, err)
	}
	if err := keys.Retire(principal.TenantID, "key-1"); err != nil {
		t.Fatal(err)
	}
	events, err = store.Audit().List(ctx, event.ExecutionID)
	if err != nil || len(events) != 1 {
		t.Fatalf("re-keyed audit read=%+v err=%v", events, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path); err == nil {
		t.Fatal("protected database opened without its protector")
	}
	reopened, err := OpenWithOptions(ctx, path, Options{
		Protector: protector, RequireProtection: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	events, err = reopened.Audit().List(ctx, "")
	if err != nil || len(events) != 1 {
		t.Fatalf("protected restart read=%+v err=%v", events, err)
	}
	if _, err := OpenWithOptions(
		ctx, filepath.Join(t.TempDir(), "required.db"),
		Options{RequireProtection: true},
	); err == nil {
		t.Fatal("expected production storage without protection to fail")
	}
}

func TestPlainSQLiteStoresDoNotClaimProtection(t *testing.T) {
	store, err := Open(
		context.Background(), filepath.Join(t.TempDir(), "plain.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Protected() {
		t.Fatal("plaintext workflow store claimed document protection")
	}
	executions, ok := store.Executions().(workflow.ProtectedExecutionStore)
	if !ok || executions.Protected() {
		t.Fatal("plaintext execution store claimed protection")
	}
	approvals, ok := store.Approvals().(policy.ProtectedApprovalStore)
	if !ok || approvals.Protected() {
		t.Fatal("plaintext approval store claimed protection")
	}
	outbox, ok := store.AuditOutbox().(audit.ProtectedOutbox)
	if !ok || outbox.Protected() {
		t.Fatal("plaintext audit outbox claimed protection")
	}
}

func TestSQLiteExecutionLeasesFenceCompetingWorkers(t *testing.T) {
	store, err := Open(
		context.Background(), filepath.Join(t.TempDir(), "leases.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	started := time.Now().UTC()
	execution, err := store.Executions().Create(ctx, workflow.Execution{
		ID: "execution-lease", WorkflowID: "workflow-1",
		WorkflowVersion: 1, Principal: principal,
		Status: workflow.ExecutionRunning,
		Input:  map[string]interface{}{}, Context: map[string]interface{}{},
		State: map[string]interface{}{},
		Steps: []workflow.StepExecution{{
			StepID: "step-1", Status: workflow.StepPending,
		}},
		StartedAt: started,
	})
	if err != nil {
		t.Fatal(err)
	}
	leases := store.LeasedExecutions()
	claim, acquired, err := leases.ClaimExecution(
		ctx, execution.ID, "worker-a", time.Minute,
	)
	if err != nil || !acquired {
		t.Fatalf("claim=%+v acquired=%v err=%v", claim, acquired, err)
	}
	if _, acquired, err := leases.ClaimExecution(
		ctx, execution.ID, "worker-b", time.Minute,
	); err != nil || acquired {
		t.Fatalf("competing claim acquired=%v err=%v", acquired, err)
	}
	execution.Steps[0].Status = workflow.StepRunning
	if _, err := leases.Update(ctx, execution); err == nil {
		t.Fatal("unclaimed update was accepted")
	}
	saved, err := leases.Update(
		workflow.WithExecutionClaim(ctx, claim), execution,
	)
	if err != nil || saved.Revision != 2 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	if err := leases.ReleaseExecution(ctx, claim); err != nil {
		t.Fatal(err)
	}
}

func TestProtectExistingPlaintextDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-protection.db")
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	legacy, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	event := audit.Event{
		ID: "legacy-event", Type: audit.EventExecutionEnded,
		Timestamp: time.Now().UTC(), TenantID: principal.TenantID,
		Outcome: "legacy-sensitive-value",
	}
	if err := legacy.Audit().Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	keys, err := sdkstorage.NewStaticKeyProvider(
		map[string]sdkstorage.EncryptionKey{
			principal.TenantID: {
				ID: "key-1", Bytes: bytes.Repeat([]byte{9}, 32),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := sdkstorage.NewAESGCMProtector(keys)
	if err != nil {
		t.Fatal(err)
	}
	migrating, err := OpenWithOptions(ctx, path, Options{
		Protector: protector, RequireProtection: true,
		AllowUnprotectedReads: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := migrating.ProtectExistingDocuments(ctx)
	if err != nil || count != 1 {
		t.Fatalf("protected rows=%d err=%v", count, err)
	}
	if err := migrating.Close(); err != nil {
		t.Fatal(err)
	}
	protected, err := OpenWithOptions(ctx, path, Options{
		Protector: protector, RequireProtection: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer protected.Close()
	events, err := protected.Audit().List(ctx, "")
	if err != nil || len(events) != 1 ||
		events[0].Outcome != event.Outcome {
		t.Fatalf("migrated events=%+v err=%v", events, err)
	}
}

func TestRetentionPurgesOnlyEligibleTenantRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.db")
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	started := time.Now().UTC()
	completed := workflow.Execution{
		ID: "completed-old", WorkflowID: "workflow",
		WorkflowVersion: 1, Principal: principal,
		Status: workflow.ExecutionCompleted,
		Input:  map[string]interface{}{}, State: map[string]interface{}{},
		Steps: []workflow.StepExecution{{
			StepID: "done", Status: workflow.StepCompleted,
		}},
		NextStep: 1, StartedAt: started,
		CompletedAt: started,
	}
	if _, err := store.Executions().Create(ctx, completed); err != nil {
		t.Fatal(err)
	}
	pendingApproval, err := store.ApprovalLifecycle().Request(
		ctx, policy.Approval{
			ExecutionID: "running", StepID: "post",
			RequestedAt: started,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	decidedApproval, err := store.ApprovalLifecycle().Request(
		ctx, policy.Approval{
			ExecutionID: "completed-old", StepID: "post",
			RequestedAt: started,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApprovalLifecycle().Decide(
		ctx, decidedApproval.ID, policy.ApprovalRejected,
		principal, "rejected",
	); err != nil {
		t.Fatal(err)
	}
	delivered := audit.Event{
		ID: "delivered-old", Type: audit.EventExecutionEnded,
		Timestamp: started, TenantID: principal.TenantID,
	}
	pending := audit.Event{
		ID: "pending-old", Type: audit.EventExecutionStarted,
		Timestamp: started, TenantID: principal.TenantID,
	}
	for _, event := range []audit.Event{delivered, pending} {
		if err := store.AuditOutbox().Enqueue(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AuditOutbox().MarkDelivered(
		ctx, delivered.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(
		ctx, `UPDATE audit_outbox SET created_at = ?`,
		started.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
	result, err := store.PurgeExpired(
		ctx, sdkstorage.RetentionPolicy{
			TerminalExecutions: time.Hour,
			DecidedApprovals:   time.Hour,
			DeliveredAudit:     time.Hour,
		},
		started.Add(2*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.TerminalExecutions != 1 ||
		result.DecidedApprovals != 1 ||
		result.DeliveredAudit != 1 {
		t.Fatalf("unexpected retention result: %+v", result)
	}
	if _, exists, err := store.ApprovalLifecycle().Get(
		ctx, pendingApproval.ID,
	); err != nil || !exists {
		t.Fatalf("pending approval was purged: exists=%t err=%v", exists, err)
	}
	pendingDeliveries, err := store.AuditOutbox().Pending(ctx, 10)
	if err != nil || len(pendingDeliveries) != 1 ||
		pendingDeliveries[0].Event.ID != pending.ID {
		t.Fatalf(
			"pending audit was purged: %+v err=%v",
			pendingDeliveries, err,
		)
	}
}

func TestSchemaMigrationsOutboxAndApprovalLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrations.db")
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := store.db.QueryRowContext(
		ctx, `PRAGMA user_version`,
	).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion {
		t.Fatalf(
			"schema version = %d, want %d", version, CurrentSchemaVersion,
		)
	}
	event := audit.Event{
		ID: "outbox-1", Type: audit.EventToolCompleted,
		Timestamp: time.Now().UTC(), TenantID: principal.TenantID,
		ActorID: principal.ActorID,
	}
	if err := store.AuditOutbox().Enqueue(ctx, event); err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApprovalLifecycle().Request(ctx, policy.Approval{
		ExecutionID: "exec-expiring", StepID: "post", Summary: "Post",
		Risk: core.RiskHigh, RequestedAt: time.Now().UTC().Add(-time.Hour),
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := store.ApprovalLifecycle().Expire(
		ctx, approval.ID, principal, "deadline elapsed",
	)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status != policy.ApprovalExpired {
		t.Fatalf("approval status = %q", expired.Status)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending, err := reopened.AuditOutbox().Pending(ctx, 10)
	if err != nil || len(pending) != 1 ||
		pending[0].Event.ID != event.ID {
		t.Fatalf("outbox restart state = %+v err=%v", pending, err)
	}
	now := time.Now().UTC()
	claimed, err := reopened.LeasedAuditOutbox().Claim(
		ctx, audit.LeaseRequest{
			WorkerID: "worker-1", Limit: 10,
			LeaseDuration: time.Minute, Now: now,
		},
	)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("leased outbox claim = %+v err=%v", claimed, err)
	}
	duplicate, err := reopened.LeasedAuditOutbox().Claim(
		ctx, audit.LeaseRequest{
			WorkerID: "worker-2", Limit: 10,
			LeaseDuration: time.Minute, Now: now,
		},
	)
	if err != nil || len(duplicate) != 0 {
		t.Fatalf("live SQLite lease was duplicated: %+v err=%v", duplicate, err)
	}
	if err := reopened.LeasedAuditOutbox().Fail(
		ctx, event.ID, audit.DeliveryFailure{
			WorkerID: "worker-1", At: now,
			Error: "sink unavailable", DeadLetter: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	dead, err := reopened.LeasedAuditOutbox().DeadLetters(ctx, 10)
	if err != nil || len(dead) != 1 ||
		dead[0].Event.ID != event.ID {
		t.Fatalf("SQLite dead letters = %+v err=%v", dead, err)
	}
	pending, err = reopened.AuditOutbox().Pending(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("dead letter remained pending: %+v err=%v", pending, err)
	}
}

func TestSchemaMigrationRejectsFutureDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`PRAGMA user_version = 999`,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path); err == nil {
		t.Fatal("expected future schema version to be rejected")
	}
}

func TestStoresPersistWorkflowLearningArtifactsAndEnforceTenant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.db")
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	version := workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow: workflow.Workflow{
			ID: "invoice", TenantID: principal.TenantID, Name: "Invoice",
		},
		Version: 1, Status: workflow.VersionCandidate, CreatedAt: time.Now(),
		Learning: &workflow.LearningMetadata{
			DemonstrationCount: 2, SequenceConsistency: 1, CommonActionCount: 1,
			ParameterCandidateCount: 1, StepEvidenceCoverage: 1, RequiresHumanReview: true,
		},
		Steps: []workflow.Step{{
			ID: "validate", Kind: workflow.StepValidation,
			Evidence: []workflow.EvidenceRef{{
				DemonstrationID: "demo-source", EventIDs: []string{"event-source"},
			}},
			Validation: &workflow.Validation{Condition: workflow.Condition{
				Left: workflow.Value{Literal: 1.0}, Operator: workflow.OpEqual, Right: workflow.Value{Literal: 1.0},
			}},
		}},
	}
	if _, err := store.Workflows().SaveCandidate(ctx, version); err != nil {
		t.Fatal(err)
	}
	review, err := workflow.NewReview(
		version, workflow.ReviewApproved, principal, "checked candidate", time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Reviews().Save(ctx, review); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Workflows().Publish(ctx, "invoice", 1, principal); err != nil {
		t.Fatal(err)
	}
	execution := workflow.Execution{
		ID: "execution-1", WorkflowID: "invoice", WorkflowVersion: 1,
		Principal: principal, Status: workflow.ExecutionAwaitingApproval,
		Input: map[string]interface{}{
			"invoice": map[string]interface{}{"id": "INV-1"},
		},
		State:             map[string]interface{}{},
		Steps:             []workflow.StepExecution{{StepID: "validate", Status: workflow.StepAwaitingApproval}},
		PendingApprovalID: "approval-recovery",
		StartedAt:         time.Now().UTC(),
	}
	execution, err = store.Executions().Create(ctx, execution)
	if err != nil {
		t.Fatal(err)
	}

	recorder, err := observation.NewRecorder(store.Demonstrations())
	if err != nil {
		t.Fatal(err)
	}
	demo, err := recorder.Start(ctx, "invoice", principal, nil)
	if err != nil {
		t.Fatal(err)
	}
	capturedEvent, err := recorder.Capture(ctx, demo.ID, observation.Event{
		ID:     "business-event-1",
		Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent, Action: "open_invoice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Capture(ctx, demo.ID, capturedEvent); !errors.Is(
		err, &core.SkawldError{Kind: core.ErrorConflict},
	) {
		t.Fatalf("duplicate SQLite observation error = %v, want conflict", err)
	}
	if _, err := recorder.Complete(ctx, demo.ID, map[string]interface{}{"ok": true}); err != nil {
		t.Fatal(err)
	}

	approval, err := store.Approvals().Request(ctx, policy.Approval{
		TenantID: principal.TenantID, ExecutionID: "exec-1", StepID: "post", Summary: "Post invoice",
		Risk: core.RiskHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approvals().Decide(ctx, approval.ID, policy.ApprovalGranted, principal, "reviewed"); err != nil {
		t.Fatal(err)
	}
	if err := store.Audit().Append(ctx, audit.Event{
		ID: "audit-1", Type: audit.EventApprovalDecided, Timestamp: time.Now(),
		TenantID: principal.TenantID, ExecutionID: "exec-1", ApprovalID: approval.ID,
	}); err != nil {
		t.Fatal(err)
	}
	evaluationReport := evaluation.Report{
		SchemaVersion: evaluation.SchemaVersion, ID: "evaluation-1", TenantID: principal.TenantID,
		SuiteName: "invoice-regression", WorkflowID: "invoice", WorkflowVersion: 1,
		WorkflowDigest: "fixture-digest",
		StartedAt:      time.Now(), CompletedAt: time.Now(),
		Gates: evaluation.GateResult{Passed: true},
		Cases: []evaluation.CaseResult{{ScenarioID: "matching-invoice", Passed: true}},
	}
	if err := store.Evaluations().Save(ctx, evaluationReport); err != nil {
		t.Fatal(err)
	}
	agentEvaluationReport := evaluation.AgentReport{
		SchemaVersion: evaluation.SchemaVersion, ID: "agent-evaluation-1", TenantID: principal.TenantID,
		SuiteName: "agent-regression", StartedAt: time.Now(), CompletedAt: time.Now(),
		Gates: evaluation.GateResult{Passed: true},
		Cases: []evaluation.AgentCaseResult{{ScenarioID: "support-ticket", Passed: true}},
	}
	if err := store.AgentEvaluations().SaveAgentReport(ctx, agentEvaluationReport); err != nil {
		t.Fatal(err)
	}
	extractorEvaluationReport := evaluation.ExtractorReport{
		SchemaVersion: evaluation.SchemaVersion, ID: "extractor-evaluation-1", TenantID: principal.TenantID,
		SuiteName: "extractor-regression", StartedAt: time.Now(), CompletedAt: time.Now(),
		Gates: evaluation.GateResult{Passed: true},
		Cases: []evaluation.ExtractorCaseResult{{ScenarioID: "invoice-traces", Passed: true}},
	}
	if err := store.ExtractorEvaluations().SaveExtractorReport(ctx, extractorEvaluationReport); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if published, ok, err := reopened.Workflows().Published(ctx, "invoice"); err != nil || !ok ||
		published.Status != workflow.VersionPublished || published.Learning == nil ||
		published.Learning.DemonstrationCount != 2 || len(published.Steps[0].Evidence) != 1 {
		t.Fatalf("published workflow did not persist: ok=%t version=%+v err=%v", ok, published, err)
	}
	loadedExecution, ok, err := reopened.Executions().Get(ctx, execution.ID)
	if err != nil || !ok || loadedExecution.Status != workflow.ExecutionAwaitingApproval ||
		loadedExecution.Revision != 1 {
		t.Fatalf(
			"workflow execution did not persist: ok=%t execution=%+v err=%v",
			ok, loadedExecution, err,
		)
	}
	loadedExecution.Status = workflow.ExecutionRunning
	loadedExecution.PendingApprovalID = ""
	loadedExecution.Steps[0].Status = workflow.StepRunning
	updatedExecution, err := reopened.Executions().Update(ctx, loadedExecution)
	if err != nil || updatedExecution.Revision != 2 {
		t.Fatalf("update workflow execution: execution=%+v err=%v", updatedExecution, err)
	}
	updatedExecution.Status = workflow.ExecutionCompleted
	updatedExecution.NextStep = 1
	updatedExecution.Steps[0].Status = workflow.StepCompleted
	updatedExecution.CompletedAt = time.Now().UTC()
	updatedExecution, err = reopened.Executions().Update(ctx, updatedExecution)
	if err != nil || updatedExecution.Revision != 3 {
		t.Fatalf("complete workflow execution: execution=%+v err=%v", updatedExecution, err)
	}
	if loaded, ok, err := reopened.Demonstrations().Get(ctx, demo.ID); err != nil || !ok || loaded.Status != observation.DemonstrationCompleted {
		t.Fatalf("demonstration did not persist: ok=%t demo=%+v err=%v", ok, loaded, err)
	}
	if loaded, ok, err := reopened.Approvals().Get(ctx, approval.ID); err != nil || !ok || loaded.Status != policy.ApprovalGranted {
		t.Fatalf("approval did not persist: ok=%t approval=%+v err=%v", ok, loaded, err)
	}
	if events, err := reopened.Audit().List(ctx, "exec-1"); err != nil || len(events) != 1 {
		t.Fatalf("audit did not persist: events=%+v err=%v", events, err)
	}
	if loaded, ok, err := reopened.Evaluations().Get(ctx, evaluationReport.ID); err != nil || !ok ||
		loaded.WorkflowID != "invoice" {
		t.Fatalf("evaluation report did not persist: ok=%t report=%+v err=%v", ok, loaded, err)
	}
	if loaded, ok, err := reopened.Reviews().Get(ctx, review.ID); err != nil || !ok ||
		loaded.Decision != workflow.ReviewApproved ||
		loaded.CandidateDigest != review.CandidateDigest {
		t.Fatalf("workflow review did not persist: ok=%t review=%+v err=%v", ok, loaded, err)
	}
	if loaded, ok, err := reopened.AgentEvaluations().GetAgentReport(ctx, agentEvaluationReport.ID); err != nil || !ok ||
		loaded.SuiteName != agentEvaluationReport.SuiteName {
		t.Fatalf("agent evaluation report did not persist: ok=%t report=%+v err=%v", ok, loaded, err)
	}
	if loaded, ok, err := reopened.ExtractorEvaluations().GetExtractorReport(
		ctx, extractorEvaluationReport.ID,
	); err != nil || !ok || loaded.SuiteName != extractorEvaluationReport.SuiteName {
		t.Fatalf("extractor evaluation report did not persist: ok=%t report=%+v err=%v", ok, loaded, err)
	}

	other := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-b", ActorID: "actor-b"})
	if _, _, err := reopened.Workflows().Get(other, "invoice", 1); err == nil {
		t.Fatal("cross-tenant workflow read was not rejected")
	}
	if _, _, err := reopened.Approvals().Get(other, approval.ID); err == nil {
		t.Fatal("cross-tenant approval read was not rejected")
	}
	if _, _, err := reopened.Executions().Get(other, execution.ID); err == nil {
		t.Fatal("cross-tenant execution read was not rejected")
	}
	if _, _, err := reopened.Reviews().Get(other, review.ID); err == nil {
		t.Fatal("cross-tenant workflow review read was not rejected")
	}
	if executions, err := reopened.Executions().List(other, workflow.ExecutionFilter{}); err != nil ||
		len(executions) != 0 {
		t.Fatalf(
			"cross-tenant execution listing leaked checkpoints: executions=%+v err=%v",
			executions, err,
		)
	}
	if events, err := reopened.Audit().List(other, "exec-1"); err != nil || len(events) != 0 {
		t.Fatalf("cross-tenant audit listing leaked events: events=%+v err=%v", events, err)
	}
	if reports, err := reopened.Evaluations().List(other, "invoice", 1); err != nil || len(reports) != 0 {
		t.Fatalf("cross-tenant evaluation listing leaked reports: reports=%+v err=%v", reports, err)
	}
	if reports, err := reopened.AgentEvaluations().ListAgentReports(other, ""); err != nil || len(reports) != 0 {
		t.Fatalf("cross-tenant agent evaluation listing leaked reports: reports=%+v err=%v", reports, err)
	}
	if reports, err := reopened.ExtractorEvaluations().ListExtractorReports(other, ""); err != nil || len(reports) != 0 {
		t.Fatalf("cross-tenant extractor evaluation listing leaked reports: reports=%+v err=%v", reports, err)
	}
}

func TestRouteAndFeedbackStoresPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow-p2.db")
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	route, err := store.Routes().Save(ctx, workflow.Route{
		TaskType: "invoice.reconcile", WorkflowID: "invoice-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	route.WorkflowID = "invoice-v2"
	route, err = store.Routes().Save(ctx, route)
	if err != nil {
		t.Fatal(err)
	}
	if route.Revision != 2 {
		t.Fatalf("route revision = %d, want 2", route.Revision)
	}
	now := time.Now().UTC()
	execution := workflow.Execution{
		ID: "execution-feedback-1", WorkflowID: "invoice-v2", WorkflowVersion: 2,
		Principal: principal, Status: workflow.ExecutionCompleted,
		Steps: []workflow.StepExecution{{
			StepID: "validate", Status: workflow.StepCompleted,
		}},
		StartedAt: now, CompletedAt: now,
	}
	feedback, err := workflow.NewExecutionFeedback(
		execution,
		workflow.FeedbackRequest{
			Disposition: workflow.FeedbackCorrection, StepID: "validate",
			ReasonCode:      "incorrect.account",
			CorrectedAction: "select_payable_account",
		},
		principal, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Feedback().Save(ctx, feedback); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	storedRoute, exists, err := reopened.Routes().Get(ctx, route.TaskType)
	if err != nil || !exists || storedRoute.WorkflowID != "invoice-v2" ||
		storedRoute.Revision != 2 {
		t.Fatalf("stored route: exists=%t route=%+v err=%v", exists, storedRoute, err)
	}
	storedFeedback, exists, err := reopened.Feedback().Get(ctx, feedback.ID)
	if err != nil || !exists ||
		storedFeedback.CorrectedAction != feedback.CorrectedAction {
		t.Fatalf(
			"stored feedback: exists=%t feedback=%+v err=%v",
			exists, storedFeedback, err,
		)
	}
	otherTenant := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-b", ActorID: "operator-b",
	})
	if _, exists, err := reopened.Routes().Get(
		otherTenant, route.TaskType,
	); err != nil || exists {
		t.Fatalf("cross-tenant route leaked: exists=%t err=%v", exists, err)
	}
	if _, exists, err := reopened.Feedback().Get(
		otherTenant, feedback.ID,
	); err != nil || exists {
		t.Fatalf("cross-tenant feedback leaked: exists=%t err=%v", exists, err)
	}
}

func TestExecutionStoreRejectsStaleUpdatesAndHonorsCancellation(t *testing.T) {
	ctx := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	})
	store, err := Open(ctx, filepath.Join(t.TempDir(), "executions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checkpoint := workflow.Execution{
		ID: "execution-1", WorkflowID: "invoice", WorkflowVersion: 1,
		Principal: core.Principal{TenantID: "tenant-a", ActorID: "operator"},
		Status:    workflow.ExecutionRunning,
		State:     map[string]interface{}{},
		Steps: []workflow.StepExecution{{
			StepID: "lookup", Status: workflow.StepRunning,
		}},
		StartedAt: time.Now().UTC(),
	}
	created, err := store.Executions().Create(ctx, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	stale := created
	created.Status = workflow.ExecutionCompleted
	created.NextStep = 1
	created.Steps[0].Status = workflow.StepCompleted
	created.CompletedAt = time.Now().UTC()
	if _, err := store.Executions().Update(ctx, created); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Executions().Update(ctx, stale); err == nil {
		t.Fatal("stale execution update was accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := store.Executions().Get(canceled, checkpoint.ID); err == nil {
		t.Fatal("canceled execution read was accepted")
	}
}

func TestExecutorResumesSQLiteApprovalCheckpointAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	firstStore, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	runner := &restartToolRunner{}
	firstExecutor, err := workflow.NewExecutor(workflow.ExecutorOptions{
		Tools: runner, Approvals: firstStore.Approvals(),
		Audit: firstStore.Audit(), Executions: firstStore.Executions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	idempotencyKey := workflow.Value{Ref: "input.invoice_id"}
	version := workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow: workflow.Workflow{
			ID: "invoice", TenantID: principal.TenantID, Name: "Invoice",
		},
		Version: 1, Status: workflow.VersionPublished, CreatedAt: time.Now().UTC(),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"invoice_id": map[string]interface{}{"type": "string"},
			},
		},
		Steps: []workflow.Step{{
			ID: "post", Kind: workflow.StepTool,
			Tool: &workflow.ToolCall{
				Name: "invoice.post",
				Arguments: map[string]workflow.Value{
					"invoice_id": {Ref: "input.invoice_id"},
				},
				IdempotencyKey: &idempotencyKey,
			},
		}},
	}
	checkpoint, err := firstExecutor.Execute(
		ctx, version, map[string]interface{}{"invoice_id": "INV-1"}, nil, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Status != workflow.ExecutionAwaitingApproval || runner.calls != 0 {
		t.Fatalf("unexpected initial checkpoint: %+v calls=%d", checkpoint, runner.calls)
	}
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, ok, err := reopened.Executions().Get(ctx, checkpoint.ID)
	if err != nil || !ok {
		t.Fatalf("load checkpoint after reopen: ok=%t err=%v", ok, err)
	}
	if _, err := reopened.Approvals().Decide(
		ctx, loaded.PendingApprovalID, policy.ApprovalGranted, principal, "reviewed",
	); err != nil {
		t.Fatal(err)
	}
	restartedExecutor, err := workflow.NewExecutor(workflow.ExecutorOptions{
		Tools: runner, Approvals: reopened.Approvals(),
		Audit: reopened.Audit(), Executions: reopened.Executions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := restartedExecutor.Resume(ctx, version, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != workflow.ExecutionCompleted || runner.calls != 1 {
		t.Fatalf("resume after reopen: execution=%+v calls=%d", completed, runner.calls)
	}
	durable, ok, err := reopened.Executions().Get(ctx, completed.ID)
	if err != nil || !ok || durable.Status != workflow.ExecutionCompleted ||
		durable.Revision != completed.Revision {
		t.Fatalf("load terminal checkpoint: ok=%t execution=%+v err=%v", ok, durable, err)
	}
}

type restartToolRunner struct {
	calls int
}

func (r *restartToolRunner) Describe(
	ctx context.Context,
	name string,
) (core.ToolDescriptor, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolDescriptor{}, false, err
	}
	if name != "invoice.post" {
		return core.ToolDescriptor{}, false, nil
	}
	return core.ToolDescriptor{
		Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
		Idempotency: core.IdempotencyRequired,
	}, true, nil
}

func (r *restartToolRunner) Execute(
	ctx context.Context,
	_ string,
	_ map[string]interface{},
	idempotencyKey string,
) (workflow.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return workflow.ToolResult{}, err
	}
	if idempotencyKey == "" {
		return workflow.ToolResult{}, core.NewConfigError("idempotency key is required")
	}
	r.calls++
	return workflow.ToolResult{
		Output: map[string]interface{}{"status": "posted"},
	}, nil
}
