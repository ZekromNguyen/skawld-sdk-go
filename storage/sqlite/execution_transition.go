package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func (s executionStore) AtomicWith(outbox audit.Outbox) bool {
	value, ok := outbox.(auditOutbox)
	return ok && value.db == s.db
}

func (s executionStore) CreateWithEvents(
	ctx context.Context,
	execution workflow.Execution,
	events []audit.Event,
) (workflow.Execution, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return workflow.Execution{}, core.NewPermissionError(
			"workflow execution storage requires an authenticated tenant",
		)
	}
	if execution.Principal.TenantID != principal.TenantID {
		return workflow.Execution{}, core.NewPermissionError(
			"workflow execution belongs to another tenant",
		)
	}
	if execution.Revision != 0 {
		return workflow.Execution{}, core.NewConfigError(
			"new workflow execution revision must be zero",
		)
	}
	execution.Revision = 1
	execution.UpdatedAt = time.Now().UTC()
	if err := execution.ValidateCheckpoint(); err != nil {
		return workflow.Execution{}, err
	}
	raw, err := s.documents.marshal(ctx, execution)
	if err != nil {
		return workflow.Execution{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workflow.Execution{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(
		ctx, `INSERT INTO workflow_executions(
			id, tenant_id, workflow_id, workflow_version, status,
			revision, updated_at, document_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		execution.ID, execution.Principal.TenantID, execution.WorkflowID,
		execution.WorkflowVersion, execution.Status, execution.Revision,
		execution.UpdatedAt.Format(time.RFC3339Nano), raw,
	); err != nil {
		return workflow.Execution{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "create workflow execution", Cause: err,
		}
	}
	if err := s.enqueueTransitionEvents(ctx, tx, events); err != nil {
		return workflow.Execution{}, err
	}
	if err := tx.Commit(); err != nil {
		return workflow.Execution{}, err
	}
	return execution, nil
}

func (s executionStore) UpdateWithEvents(
	ctx context.Context,
	execution workflow.Execution,
	events []audit.Event,
) (workflow.Execution, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return workflow.Execution{}, core.NewPermissionError(
			"workflow execution storage requires an authenticated tenant",
		)
	}
	if execution.Principal.TenantID != principal.TenantID {
		return workflow.Execution{}, core.NewPermissionError(
			"workflow execution belongs to another tenant",
		)
	}
	if execution.Revision < 1 {
		return workflow.Execution{}, core.NewConfigError(
			"workflow execution update requires a revision",
		)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workflow.Execution{}, err
	}
	defer tx.Rollback()
	var currentRaw []byte
	var leaseOwner, leaseUntilRaw string
	var leaseToken int64
	err = tx.QueryRowContext(
		ctx,
		`SELECT document_json, lease_owner, lease_until, lease_token
			FROM workflow_executions WHERE id = ?`,
		execution.ID,
	).Scan(&currentRaw, &leaseOwner, &leaseUntilRaw, &leaseToken)
	if err == sql.ErrNoRows {
		return workflow.Execution{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution not found",
		}
	}
	if err != nil {
		return workflow.Execution{}, err
	}
	var current workflow.Execution
	if err := s.documents.unmarshal(ctx, currentRaw, &current); err != nil {
		return workflow.Execution{}, err
	}
	if current.Principal.TenantID != principal.TenantID {
		return workflow.Execution{}, core.NewPermissionError(
			"workflow execution belongs to another tenant",
		)
	}
	if leaseOwner != "" {
		leaseUntil, err := time.Parse(time.RFC3339Nano, leaseUntilRaw)
		if err != nil {
			return workflow.Execution{}, core.NewConfigError(
				"workflow execution lease timestamp is invalid",
			)
		}
		claim, claimed := workflow.ExecutionClaimFromContext(ctx)
		if !time.Now().UTC().Before(leaseUntil) ||
			!claimed || claim.Execution.ID != execution.ID ||
			claim.Owner != leaseOwner || claim.Token != leaseToken {
			return workflow.Execution{}, &core.SkawldError{
				Kind:    core.ErrorConflict,
				Message: "workflow execution lease is absent, expired, or owned by another worker",
			}
		}
	}
	if err := workflow.ValidateExecutionUpdate(
		current, execution,
	); err != nil {
		return workflow.Execution{}, err
	}
	previousRevision := execution.Revision
	execution.Revision++
	execution.UpdatedAt = time.Now().UTC()
	if err := execution.ValidateCheckpoint(); err != nil {
		return workflow.Execution{}, err
	}
	raw, err := s.documents.marshal(ctx, execution)
	if err != nil {
		return workflow.Execution{}, err
	}
	result, err := tx.ExecContext(
		ctx, `UPDATE workflow_executions
			SET status = ?, revision = ?, updated_at = ?, document_json = ?
			WHERE id = ? AND tenant_id = ? AND workflow_id = ?
				AND workflow_version = ? AND revision = ?`,
		execution.Status, execution.Revision,
		execution.UpdatedAt.Format(time.RFC3339Nano), raw,
		execution.ID, execution.Principal.TenantID, execution.WorkflowID,
		execution.WorkflowVersion, previousRevision,
	)
	if err != nil {
		return workflow.Execution{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return workflow.Execution{}, err
	}
	if affected != 1 {
		return workflow.Execution{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution revision conflict",
		}
	}
	if err := s.enqueueTransitionEvents(ctx, tx, events); err != nil {
		return workflow.Execution{}, err
	}
	if err := tx.Commit(); err != nil {
		return workflow.Execution{}, err
	}
	return execution, nil
}

func (s executionStore) enqueueTransitionEvents(
	ctx context.Context,
	tx *sql.Tx,
	events []audit.Event,
) error {
	for _, event := range events {
		if event.ID == "" || event.Type == "" ||
			event.Timestamp.IsZero() || event.TenantID == "" {
			return core.NewConfigError(
				"audit outbox event requires id, type, timestamp, and tenant",
			)
		}
		principal, err := storageActor(ctx, "audit outbox")
		if err != nil {
			return err
		}
		if event.TenantID != principal.TenantID {
			return core.NewPermissionError(
				"audit outbox event belongs to another tenant",
			)
		}
		raw, err := s.documents.marshal(ctx, event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx, `INSERT INTO audit_outbox(
				id, tenant_id, created_at, document_json
			) VALUES (?, ?, ?, ?)`,
			event.ID, event.TenantID,
			time.Now().UTC().Format(time.RFC3339Nano), raw,
		); err != nil {
			return err
		}
	}
	return nil
}

var _ workflow.ExecutionTransitionStore = executionStore{}
