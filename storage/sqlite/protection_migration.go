package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	sdkstorage "github.com/ZekromNguyen/skawld-sdk-go/storage"
)

var protectedDocumentTables = []string{
	"workflow_versions",
	"workflow_executions",
	"demonstrations",
	"approvals",
	"audit_events",
	"audit_outbox",
	"evaluation_reports",
	"workflow_reviews",
	"agent_evaluation_reports",
	"extractor_evaluation_reports",
	"workflow_feedback",
}

// ProtectExistingDocuments atomically encrypts legacy plaintext JSON rows for
// the authenticated tenant. The configured protector must be able to identify
// its own envelopes so wrong-key ciphertext is never double-encrypted.
func (s *Store) ProtectExistingDocuments(
	ctx context.Context,
) (int64, error) {
	principal, err := storageActor(ctx, "document protection migration")
	if err != nil {
		return 0, err
	}
	if s.documents.protector == nil {
		return 0, core.NewConfigError(
			"document protection migration requires a protector",
		)
	}
	detector, ok := s.documents.protector.(sdkstorage.ProtectedDocumentDetector)
	if !ok {
		return 0, core.NewConfigError(
			"document protector cannot identify existing protected envelopes",
		)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var protectedCount int64
	for _, table := range protectedDocumentTables {
		rows, err := tx.QueryContext(
			ctx, `SELECT rowid, document_json FROM `+table+
				` WHERE tenant_id = ?`,
			principal.TenantID,
		)
		if err != nil {
			return 0, err
		}
		type document struct {
			rowID int64
			raw   []byte
		}
		documents := make([]document, 0)
		for rows.Next() {
			var item document
			if err := rows.Scan(&item.rowID, &item.raw); err != nil {
				rows.Close()
				return 0, err
			}
			documents = append(documents, item)
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
		if err := rows.Err(); err != nil {
			return 0, err
		}
		for _, item := range documents {
			if detector.IsProtected(item.raw) {
				continue
			}
			if !json.Valid(item.raw) {
				return 0, &core.SkawldError{
					Kind: core.ErrorValidation,
					Message: fmt.Sprintf(
						"%s row %d is neither plaintext JSON nor a recognized protected envelope",
						table, item.rowID,
					),
				}
			}
			protected, err := s.documents.protector.Protect(
				ctx, item.raw,
			)
			if err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(
				ctx, `UPDATE `+table+
					` SET document_json = ? WHERE rowid = ?`,
				protected, item.rowID,
			); err != nil {
				return 0, err
			}
			protectedCount++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return protectedCount, nil
}

// RotateProtectedDocuments atomically decrypts and re-protects every document
// for the authenticated tenant using the protector's current key. Plaintext or
// unrecognized rows fail closed; use ProtectExistingDocuments first for a
// legacy database. Historical keys must remain available until this succeeds.
func (s *Store) RotateProtectedDocuments(
	ctx context.Context,
) (int64, error) {
	principal, err := storageActor(ctx, "document protection rotation")
	if err != nil {
		return 0, err
	}
	if s.documents.protector == nil {
		return 0, core.NewConfigError(
			"document protection rotation requires a protector",
		)
	}
	detector, ok := s.documents.protector.(sdkstorage.ProtectedDocumentDetector)
	if !ok {
		return 0, core.NewConfigError(
			"document protector cannot identify protected envelopes",
		)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rotated int64
	for _, table := range protectedDocumentTables {
		rows, err := tx.QueryContext(
			ctx, `SELECT rowid, document_json FROM `+table+
				` WHERE tenant_id = ?`,
			principal.TenantID,
		)
		if err != nil {
			return 0, err
		}
		type document struct {
			rowID int64
			raw   []byte
		}
		documents := make([]document, 0)
		for rows.Next() {
			var item document
			if err := rows.Scan(&item.rowID, &item.raw); err != nil {
				rows.Close()
				return 0, err
			}
			documents = append(documents, item)
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
		if err := rows.Err(); err != nil {
			return 0, err
		}
		for _, item := range documents {
			if !detector.IsProtected(item.raw) {
				return 0, &core.SkawldError{
					Kind: core.ErrorValidation,
					Message: fmt.Sprintf(
						"%s row %d is not a recognized protected envelope",
						table, item.rowID,
					),
				}
			}
			plaintext, err := s.documents.protector.Unprotect(ctx, item.raw)
			if err != nil {
				return 0, err
			}
			protected, err := s.documents.protector.Protect(ctx, plaintext)
			if err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(
				ctx, `UPDATE `+table+
					` SET document_json = ? WHERE rowid = ?`,
				protected, item.rowID,
			); err != nil {
				return 0, err
			}
			rotated++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rotated, nil
}
