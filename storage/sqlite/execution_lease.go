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

func (s executionStore) ClaimReadyExecutions(
	ctx context.Context,
	request workflow.ReadyExecutionClaimRequest,
) ([]workflow.ExecutionClaim, error) {
	if err := validateExecutionLease(
		request.Owner, request.Duration,
	); err != nil {
		return nil, err
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return nil, core.NewConfigError(
			"workflow ready-claim limit must be between 1 and 1000",
		)
	}
	if len(request.Statuses) == 0 {
		return nil, core.NewConfigError(
			"workflow ready claim requires at least one status",
		)
	}
	for _, status := range request.Statuses {
		if status != workflow.ExecutionRunning &&
			status != workflow.ExecutionAwaitingApproval {
			return nil, core.NewConfigError(
				"workflow ready claim status is invalid",
			)
		}
	}
	principal, err := storageActor(ctx, "workflow execution lease")
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	query := `SELECT id, lease_token, document_json
		FROM workflow_executions
		WHERE tenant_id = ? AND status IN (`
	args := []interface{}{principal.TenantID}
	for index, status := range request.Statuses {
		if index > 0 {
			query += ","
		}
		query += "?"
		args = append(args, status)
	}
	query += `) AND (
			lease_owner = '' OR lease_until = ''
			OR julianday(lease_until) <= julianday(?)
		)
		ORDER BY updated_at ASC, id
		LIMIT ?`
	args = append(
		args, now.Format(time.RFC3339Nano), request.Limit,
	)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type readyRow struct {
		id        string
		token     int64
		execution workflow.Execution
	}
	ready := make([]readyRow, 0)
	for rows.Next() {
		var item readyRow
		var raw []byte
		if err := rows.Scan(&item.id, &item.token, &raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := s.documents.unmarshal(
			ctx, raw, &item.execution,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ready = append(ready, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	until := now.Add(request.Duration)
	claims := make([]workflow.ExecutionClaim, 0, len(ready))
	for _, item := range ready {
		token := item.token + 1
		// Re-check status in the fencing UPDATE, not just the lease columns.
		// A row read as ready in this transaction may have been terminalized
		// by another worker between the SELECT and the lease assignment;
		// claiming it would waste a slot and starve ready work.
		updateQuery := `UPDATE workflow_executions
				SET lease_owner = ?, lease_until = ?, lease_token = ?
				WHERE id = ? AND tenant_id = ? AND lease_token = ?
					AND status IN (`
		updateArgs := []interface{}{
			request.Owner, until.Format(time.RFC3339Nano), token,
			item.id, principal.TenantID, item.token,
		}
		for index, status := range request.Statuses {
			if index > 0 {
				updateQuery += ","
			}
			updateQuery += "?"
			updateArgs = append(updateArgs, status)
		}
		updateQuery += `) AND (
				lease_owner = '' OR lease_until = ''
				OR julianday(lease_until) <= julianday(?)
			)`
		updateArgs = append(
			updateArgs, now.Format(time.RFC3339Nano),
		)
		result, err := tx.ExecContext(
			ctx, updateQuery, updateArgs...,
		)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			continue
		}
		claims = append(claims, workflow.ExecutionClaim{
			Execution: item.execution, Owner: request.Owner,
			Token: token, LeaseUntil: until,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claims, nil
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
var _ workflow.ReadyExecutionClaimer = executionStore{}
