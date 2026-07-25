package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	sdkstorage "github.com/ZekromNguyen/skawld-sdk-go/storage"
)

func (s *Store) PurgeExpired(
	ctx context.Context,
	policy sdkstorage.RetentionPolicy,
	now time.Time,
) (sdkstorage.RetentionResult, error) {
	principal, err := storageActor(ctx, "retention")
	if err != nil {
		return sdkstorage.RetentionResult{}, err
	}
	if err := policy.Validate(); err != nil {
		return sdkstorage.RetentionResult{}, core.NewConfigError(err.Error())
	}
	if now.IsZero() {
		return sdkstorage.RetentionResult{}, core.NewConfigError(
			"retention purge requires a timestamp",
		)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sdkstorage.RetentionResult{}, err
	}
	defer tx.Rollback()
	result := sdkstorage.RetentionResult{}
	if policy.TerminalExecutions > 0 {
		result.TerminalExecutions, err = purgeRows(
			ctx, tx, `DELETE FROM workflow_executions
				WHERE tenant_id = ?
					AND status IN ('completed', 'failed', 'canceled')
					AND updated_at != '' AND updated_at < ?`,
			principal.TenantID,
			retentionCutoff(now, policy.TerminalExecutions),
		)
		if err != nil {
			return result, err
		}
	}
	if policy.Demonstrations > 0 {
		result.Demonstrations, err = purgeRows(
			ctx, tx, `DELETE FROM demonstrations
				WHERE tenant_id = ?
					AND status IN ('completed', 'rejected')
					AND updated_at != '' AND updated_at < ?`,
			principal.TenantID,
			retentionCutoff(now, policy.Demonstrations),
		)
		if err != nil {
			return result, err
		}
	}
	if policy.DecidedApprovals > 0 {
		result.DecidedApprovals, err = purgeRows(
			ctx, tx, `DELETE FROM approvals
				WHERE tenant_id = ? AND status != 'pending'
					AND updated_at != '' AND updated_at < ?`,
			principal.TenantID,
			retentionCutoff(now, policy.DecidedApprovals),
		)
		if err != nil {
			return result, err
		}
	}
	if policy.AuditEvents > 0 {
		result.AuditEvents, err = purgeRows(
			ctx, tx, `DELETE FROM audit_events
				WHERE tenant_id = ? AND timestamp < ?`,
			principal.TenantID,
			retentionCutoff(now, policy.AuditEvents),
		)
		if err != nil {
			return result, err
		}
	}
	if policy.DeliveredAudit > 0 {
		result.DeliveredAudit, err = purgeRows(
			ctx, tx, `DELETE FROM audit_outbox
				WHERE tenant_id = ?
					AND (delivered_at != '' OR dead_lettered_at != '')
					AND created_at < ?`,
			principal.TenantID,
			retentionCutoff(now, policy.DeliveredAudit),
		)
		if err != nil {
			return result, err
		}
	}
	if policy.Feedback > 0 {
		result.Feedback, err = purgeRows(
			ctx, tx, `DELETE FROM workflow_feedback
				WHERE tenant_id = ? AND created_at < ?`,
			principal.TenantID,
			retentionCutoff(now, policy.Feedback),
		)
		if err != nil {
			return result, err
		}
	}
	if policy.Reviews > 0 {
		result.Reviews, err = purgeRows(
			ctx, tx, `DELETE FROM workflow_reviews
				WHERE tenant_id = ? AND reviewed_at < ?`,
			principal.TenantID,
			retentionCutoff(now, policy.Reviews),
		)
		if err != nil {
			return result, err
		}
	}
	if policy.Evaluations > 0 {
		cutoff := retentionCutoff(now, policy.Evaluations)
		for _, table := range []string{
			"evaluation_reports",
			"agent_evaluation_reports",
			"extractor_evaluation_reports",
		} {
			count, purgeErr := purgeRows(
				ctx, tx, `DELETE FROM `+table+
					` WHERE tenant_id = ? AND started_at < ?`,
				principal.TenantID, cutoff,
			)
			if purgeErr != nil {
				return result, purgeErr
			}
			result.Evaluations += count
		}
	}
	if err := tx.Commit(); err != nil {
		return sdkstorage.RetentionResult{}, err
	}
	return result, nil
}

func purgeRows(
	ctx context.Context,
	tx *sql.Tx,
	query string,
	args ...interface{},
) (int64, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func retentionCutoff(now time.Time, retention time.Duration) string {
	return now.UTC().Add(-retention).Format(time.RFC3339Nano)
}
