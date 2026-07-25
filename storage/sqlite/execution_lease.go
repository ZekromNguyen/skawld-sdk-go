package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func (s executionStore) ClaimExecution(
	ctx context.Context,
	executionID string,
	owner string,
	duration time.Duration,
) (workflow.ExecutionClaim, bool, error) {
	if err := validateExecutionLease(owner, duration); err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	principal, err := storageActor(ctx, "workflow execution lease")
	if err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	defer tx.Rollback()
	var raw []byte
	var status workflow.ExecutionStatus
	var currentOwner, currentUntilRaw string
	var currentToken int64
	err = tx.QueryRowContext(
		ctx, `SELECT status, lease_owner, lease_until, lease_token,
				document_json
			FROM workflow_executions
			WHERE id = ? AND tenant_id = ?`,
		executionID, principal.TenantID,
	).Scan(
		&status, &currentOwner, &currentUntilRaw, &currentToken, &raw,
	)
	if err == sql.ErrNoRows {
		return workflow.ExecutionClaim{}, false, nil
	}
	if err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	if status == workflow.ExecutionCompleted ||
		status == workflow.ExecutionFailed ||
		status == workflow.ExecutionCanceled {
		return workflow.ExecutionClaim{}, false, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "terminal workflow execution cannot be claimed",
		}
	}
	now := time.Now().UTC()
	currentUntil, err := parseOptionalLeaseTime(currentUntilRaw)
	if err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	if currentOwner != "" && now.Before(currentUntil) &&
		currentOwner != owner {
		return workflow.ExecutionClaim{}, false, nil
	}
	token := currentToken
	if currentOwner != owner || !now.Before(currentUntil) {
		token++
	}
	until := now.Add(duration)
	result, err := tx.ExecContext(
		ctx, `UPDATE workflow_executions
			SET lease_owner = ?, lease_until = ?, lease_token = ?
			WHERE id = ? AND tenant_id = ? AND lease_token = ?`,
		owner, until.Format(time.RFC3339Nano), token,
		executionID, principal.TenantID, currentToken,
	)
	if err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	if affected != 1 {
		return workflow.ExecutionClaim{}, false, nil
	}
	var execution workflow.Execution
	if err := s.documents.unmarshal(ctx, raw, &execution); err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return workflow.ExecutionClaim{}, false, err
	}
	return workflow.ExecutionClaim{
		Execution: execution, Owner: owner,
		Token: token, LeaseUntil: until,
	}, true, nil
}

func (s executionStore) RenewExecution(
	ctx context.Context,
	claim workflow.ExecutionClaim,
	duration time.Duration,
) (workflow.ExecutionClaim, error) {
	if err := validateExecutionLease(claim.Owner, duration); err != nil {
		return workflow.ExecutionClaim{}, err
	}
	principal, err := storageActor(ctx, "workflow execution lease")
	if err != nil {
		return workflow.ExecutionClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workflow.ExecutionClaim{}, err
	}
	defer tx.Rollback()
	var currentOwner, currentUntilRaw string
	var currentToken int64
	err = tx.QueryRowContext(
		ctx, `SELECT lease_owner, lease_until, lease_token
			FROM workflow_executions
			WHERE id = ? AND tenant_id = ?`,
		claim.Execution.ID, principal.TenantID,
	).Scan(&currentOwner, &currentUntilRaw, &currentToken)
	if err == sql.ErrNoRows {
		return workflow.ExecutionClaim{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "workflow execution not found",
		}
	}
	if err != nil {
		return workflow.ExecutionClaim{}, err
	}
	currentUntil, err := parseOptionalLeaseTime(currentUntilRaw)
	if err != nil {
		return workflow.ExecutionClaim{}, err
	}
	now := time.Now().UTC()
	if currentOwner != claim.Owner || currentToken != claim.Token ||
		!now.Before(currentUntil) {
		return workflow.ExecutionClaim{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution lease was lost",
		}
	}
	until := now.Add(duration)
	result, err := tx.ExecContext(
		ctx, `UPDATE workflow_executions
			SET lease_until = ?
			WHERE id = ? AND tenant_id = ? AND lease_owner = ?
				AND lease_token = ?`,
		until.Format(time.RFC3339Nano), claim.Execution.ID,
		principal.TenantID, claim.Owner, claim.Token,
	)
	if err != nil {
		return workflow.ExecutionClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return workflow.ExecutionClaim{}, err
	}
	if affected != 1 {
		return workflow.ExecutionClaim{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution lease was lost",
		}
	}
	if err := tx.Commit(); err != nil {
		return workflow.ExecutionClaim{}, err
	}
	claim.LeaseUntil = until
	return claim, nil
}

func (s executionStore) ReleaseExecution(
	ctx context.Context,
	claim workflow.ExecutionClaim,
) error {
	principal, err := storageActor(ctx, "workflow execution lease")
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentOwner, currentUntilRaw string
	var currentToken int64
	err = tx.QueryRowContext(
		ctx, `SELECT lease_owner, lease_until, lease_token
			FROM workflow_executions
			WHERE id = ? AND tenant_id = ?`,
		claim.Execution.ID, principal.TenantID,
	).Scan(&currentOwner, &currentUntilRaw, &currentToken)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	currentUntil, err := parseOptionalLeaseTime(currentUntilRaw)
	if err != nil {
		return err
	}
	if currentOwner != claim.Owner || currentToken != claim.Token ||
		!time.Now().UTC().Before(currentUntil) {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution lease was lost",
		}
	}
	result, err := tx.ExecContext(
		ctx, `UPDATE workflow_executions
			SET lease_owner = '', lease_until = ''
			WHERE id = ? AND tenant_id = ? AND lease_owner = ?
				AND lease_token = ?`,
		claim.Execution.ID, principal.TenantID, claim.Owner, claim.Token,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution lease was lost",
		}
	}
	return tx.Commit()
}

func validateExecutionLease(owner string, duration time.Duration) error {
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 256 ||
		strings.ContainsAny(owner, "\r\n\x00") {
		return core.NewConfigError("workflow execution lease owner is invalid")
	}
	if duration < time.Second || duration > 24*time.Hour {
		return core.NewConfigError(
			"workflow execution lease duration must be between one second and 24 hours",
		)
	}
	return nil
}

func parseOptionalLeaseTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, core.NewConfigError(
			"workflow execution lease timestamp is invalid",
		)
	}
	return value, nil
}

var _ workflow.ExecutionLeaseStore = executionStore{}
