package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func (s auditOutbox) Claim(
	ctx context.Context,
	request audit.LeaseRequest,
) ([]audit.Delivery, error) {
	if err := validateSQLiteLeaseRequest(request); err != nil {
		return nil, err
	}
	principal, err := storageActor(ctx, "audit outbox")
	if err != nil {
		return nil, err
	}
	now := request.Now.UTC().Format(time.RFC3339Nano)
	leaseUntil := request.Now.UTC().Add(
		request.LeaseDuration,
	).Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT
		id, document_json, attempts, created_at, last_attempt_at,
		last_error, next_attempt_at
		FROM audit_outbox
		WHERE tenant_id = ?
			AND delivered_at = ''
			AND dead_lettered_at = ''
			AND (next_attempt_at = '' OR next_attempt_at <= ?)
			AND (lease_until = '' OR lease_until <= ?)
		ORDER BY created_at, id
		LIMIT ?`,
		principal.TenantID, now, now, request.Limit,
	)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id            string
		raw           []byte
		attempts      int
		createdAt     string
		lastAttemptAt string
		lastError     string
		nextAttemptAt string
	}
	candidates := make([]candidate, 0)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(
			&item.id, &item.raw, &item.attempts, &item.createdAt,
			&item.lastAttemptAt, &item.lastError, &item.nextAttemptAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	output := make([]audit.Delivery, 0, len(candidates))
	for _, item := range candidates {
		result, err := tx.ExecContext(ctx, `UPDATE audit_outbox
			SET lease_owner = ?, lease_until = ?
			WHERE id = ? AND tenant_id = ?
				AND delivered_at = '' AND dead_lettered_at = ''
				AND (next_attempt_at = '' OR next_attempt_at <= ?)
				AND (lease_until = '' OR lease_until <= ?)`,
			request.WorkerID, leaseUntil, item.id, principal.TenantID,
			now, now,
		)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			continue
		}
		var event audit.Event
		if err := s.documents.unmarshal(ctx, item.raw, &event); err != nil {
			return nil, err
		}
		delivery := audit.Delivery{
			Event: event, Attempts: item.attempts,
			LastError: item.lastError, LeaseOwner: request.WorkerID,
			LeaseUntil: request.Now.UTC().Add(request.LeaseDuration),
		}
		if delivery.CreatedAt, err = parseSQLiteTime(
			item.createdAt,
		); err != nil {
			return nil, err
		}
		if item.lastAttemptAt != "" {
			if delivery.LastAttemptAt, err = parseSQLiteTime(
				item.lastAttemptAt,
			); err != nil {
				return nil, err
			}
		}
		if item.nextAttemptAt != "" {
			if delivery.NextAttemptAt, err = parseSQLiteTime(
				item.nextAttemptAt,
			); err != nil {
				return nil, err
			}
		}
		output = append(output, delivery)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return output, nil
}

func (s auditOutbox) Acknowledge(
	ctx context.Context,
	eventID string,
	workerID string,
	at time.Time,
) error {
	principal, err := storageActor(ctx, "audit outbox")
	if err != nil {
		return err
	}
	if strings.TrimSpace(workerID) == "" || at.IsZero() {
		return core.NewConfigError(
			"audit acknowledgement requires worker and timestamp",
		)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE audit_outbox
		SET delivered_at = ?, lease_owner = '', lease_until = '',
			last_error = ''
		WHERE id = ? AND tenant_id = ? AND lease_owner = ?
			AND delivered_at = '' AND dead_lettered_at = ''`,
		at.UTC().Format(time.RFC3339Nano), eventID,
		principal.TenantID, workerID,
	)
	if err != nil {
		return err
	}
	return sqliteLeaseMutationResult(
		ctx, s.db, result, eventID, principal.TenantID,
	)
}

func (s auditOutbox) Fail(
	ctx context.Context,
	eventID string,
	failure audit.DeliveryFailure,
) error {
	principal, err := storageActor(ctx, "audit outbox")
	if err != nil {
		return err
	}
	if err := validateSQLiteDeliveryFailure(failure); err != nil {
		return err
	}
	message := strings.TrimSpace(failure.Error)
	if len(message) > 1024 {
		message = message[:1024]
	}
	next := ""
	dead := ""
	if failure.DeadLetter {
		dead = failure.At.UTC().Format(time.RFC3339Nano)
	} else {
		next = failure.NextAttemptAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE audit_outbox
		SET attempts = attempts + 1, last_attempt_at = ?,
			last_error = ?, next_attempt_at = ?,
			dead_lettered_at = ?, lease_owner = '', lease_until = ''
		WHERE id = ? AND tenant_id = ? AND lease_owner = ?
			AND delivered_at = '' AND dead_lettered_at = ''`,
		failure.At.UTC().Format(time.RFC3339Nano), message, next, dead,
		eventID, principal.TenantID, failure.WorkerID,
	)
	if err != nil {
		return err
	}
	return sqliteLeaseMutationResult(
		ctx, s.db, result, eventID, principal.TenantID,
	)
}

func (s auditOutbox) DeadLetters(
	ctx context.Context,
	limit int,
) ([]audit.Delivery, error) {
	principal, err := storageActor(ctx, "audit outbox")
	if err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, core.NewConfigError(
			"audit dead-letter limit must be between 1 and 1000",
		)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		document_json, attempts, created_at, last_attempt_at, last_error,
		dead_lettered_at
		FROM audit_outbox
		WHERE tenant_id = ? AND dead_lettered_at != ''
		ORDER BY dead_lettered_at, id
		LIMIT ?`, principal.TenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]audit.Delivery, 0)
	for rows.Next() {
		var raw []byte
		var delivery audit.Delivery
		var createdAt, lastAttemptAt, deadLetteredAt string
		if err := rows.Scan(
			&raw, &delivery.Attempts, &createdAt, &lastAttemptAt,
			&delivery.LastError, &deadLetteredAt,
		); err != nil {
			return nil, err
		}
		if err := s.documents.unmarshal(
			ctx, raw, &delivery.Event,
		); err != nil {
			return nil, err
		}
		if delivery.CreatedAt, err = parseSQLiteTime(
			createdAt,
		); err != nil {
			return nil, err
		}
		if delivery.DeadLetteredAt, err = parseSQLiteTime(
			deadLetteredAt,
		); err != nil {
			return nil, err
		}
		if lastAttemptAt != "" {
			if delivery.LastAttemptAt, err = parseSQLiteTime(
				lastAttemptAt,
			); err != nil {
				return nil, err
			}
		}
		output = append(output, delivery)
	}
	return output, rows.Err()
}

func (s auditOutbox) Requeue(
	ctx context.Context,
	eventID string,
	at time.Time,
) error {
	principal, err := storageActor(ctx, "audit outbox")
	if err != nil {
		return err
	}
	if at.IsZero() {
		return core.NewConfigError(
			"audit dead-letter requeue requires a timestamp",
		)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE audit_outbox
		SET attempts = 0, dead_lettered_at = '',
			next_attempt_at = ?, lease_owner = '', lease_until = ''
		WHERE id = ? AND tenant_id = ? AND dead_lettered_at != ''`,
		at.UTC().Format(time.RFC3339Nano), eventID, principal.TenantID,
	)
	if err != nil {
		return err
	}
	return sqliteLeaseMutationResult(
		ctx, s.db, result, eventID, principal.TenantID,
	)
}

func sqliteLeaseMutationResult(
	ctx context.Context,
	db *sql.DB,
	result sql.Result,
	eventID string,
	tenantID string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	var count int
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM audit_outbox
			WHERE id = ? AND tenant_id = ?`,
		eventID, tenantID,
	).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "audit outbox event not found",
		}
	}
	return &core.SkawldError{
		Kind:    core.ErrorConflict,
		Message: "audit outbox lease is owned by another worker",
	}
}

func validateSQLiteLeaseRequest(request audit.LeaseRequest) error {
	if strings.TrimSpace(request.WorkerID) == "" ||
		len(request.WorkerID) > 256 ||
		strings.ContainsAny(request.WorkerID, "\r\n\x00") ||
		request.Limit < 1 || request.Limit > 1000 ||
		request.LeaseDuration < time.Second ||
		request.LeaseDuration > 24*time.Hour ||
		request.Now.IsZero() {
		return core.NewConfigError("audit outbox lease request is invalid")
	}
	return nil
}

func validateSQLiteDeliveryFailure(
	failure audit.DeliveryFailure,
) error {
	if strings.TrimSpace(failure.WorkerID) == "" ||
		failure.At.IsZero() ||
		!failure.DeadLetter &&
			(failure.NextAttemptAt.IsZero() ||
				failure.NextAttemptAt.Before(failure.At)) {
		return core.NewConfigError("audit delivery failure is invalid")
	}
	return nil
}
