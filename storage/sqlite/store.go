// Package sqlite provides durable local persistence for workflow-learning
// domain stores. It is independent from sessions/sqlite so applications can
// adopt workflow storage without changing conversation persistence.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	sdkstorage "github.com/ZekromNguyen/skawld-sdk-go/storage"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	documents documentCodec
}

const CurrentSchemaVersion = 5

type Options struct {
	Protector             sdkstorage.DocumentProtector
	RequireProtection     bool
	AllowUnprotectedReads bool
}

func Open(ctx context.Context, path string) (*Store, error) {
	return OpenWithOptions(ctx, path, Options{})
}

func OpenWithOptions(
	ctx context.Context,
	path string,
	options Options,
) (*Store, error) {
	if path == "" {
		return nil, core.NewConfigError("workflow sqlite path is required")
	}
	if options.RequireProtection && options.Protector == nil {
		return nil, core.NewConfigError(
			"workflow sqlite production mode requires document protection",
		)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{
		db: db,
		documents: documentCodec{
			protector:             options.Protector,
			allowUnprotectedReads: options.AllowUnprotectedReads,
		},
	}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.configureProtection(ctx, options); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`, `PRAGMA busy_timeout = 5000`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure workflow sqlite: %w", err)
		}
	}
	baseStatements := []string{
		`CREATE TABLE IF NOT EXISTS workflow_versions (
			workflow_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			document_json TEXT NOT NULL,
			PRIMARY KEY (workflow_id, version)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS one_published_workflow_version
			ON workflow_versions(workflow_id) WHERE status = 'published'`,
		`CREATE TABLE IF NOT EXISTS workflow_executions (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			workflow_version INTEGER NOT NULL,
			status TEXT NOT NULL,
			revision INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS workflow_executions_lookup
			ON workflow_executions(tenant_id, workflow_id, status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS demonstrations (
			id TEXT PRIMARY KEY,
			workflow_key TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS demonstrations_workflow_key ON demonstrations(workflow_key)`,
		`CREATE TABLE IF NOT EXISTS approvals (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT '',
			execution_id TEXT NOT NULL,
			status TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS approvals_execution_id ON approvals(execution_id)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			tenant_id TEXT NOT NULL DEFAULT '',
			execution_id TEXT NOT NULL DEFAULT '',
			timestamp TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS audit_execution_id ON audit_events(execution_id, seq)`,
		`CREATE TABLE IF NOT EXISTS evaluation_reports (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			workflow_version INTEGER NOT NULL,
			started_at TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS evaluation_workflow_version
			ON evaluation_reports(workflow_id, workflow_version, started_at)`,
		`CREATE TABLE IF NOT EXISTS workflow_reviews (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			workflow_version INTEGER NOT NULL,
			reviewed_at TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS workflow_reviews_candidate
			ON workflow_reviews(workflow_id, workflow_version, reviewed_at)`,
		`CREATE TABLE IF NOT EXISTS agent_evaluation_reports (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			suite_name TEXT NOT NULL,
			started_at TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS agent_evaluation_suite
			ON agent_evaluation_reports(suite_name, started_at)`,
		`CREATE TABLE IF NOT EXISTS extractor_evaluation_reports (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			suite_name TEXT NOT NULL,
			started_at TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS extractor_evaluation_suite
			ON extractor_evaluation_reports(suite_name, started_at)`,
		`CREATE TABLE IF NOT EXISTS workflow_routes (
			tenant_id TEXT NOT NULL,
			task_type TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			updated_by TEXT NOT NULL,
			PRIMARY KEY (tenant_id, task_type)
		)`,
		`CREATE INDEX IF NOT EXISTS workflow_routes_workflow
			ON workflow_routes(tenant_id, workflow_id)`,
		`CREATE TABLE IF NOT EXISTS workflow_feedback (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			workflow_version INTEGER NOT NULL,
			disposition TEXT NOT NULL,
			created_at TEXT NOT NULL,
			document_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS workflow_feedback_lookup
			ON workflow_feedback(
				tenant_id, workflow_id, workflow_version, execution_id,
				disposition, created_at DESC
			)`,
	}
	migrations := []sqliteMigration{
		{version: 1, statements: baseStatements},
		{version: 2, statements: []string{
			`CREATE TABLE IF NOT EXISTS audit_outbox (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				created_at TEXT NOT NULL,
				attempts INTEGER NOT NULL DEFAULT 0,
				last_attempt_at TEXT NOT NULL DEFAULT '',
				last_error TEXT NOT NULL DEFAULT '',
				delivered_at TEXT NOT NULL DEFAULT '',
				document_json TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS audit_outbox_pending
				ON audit_outbox(tenant_id, delivered_at, created_at, id)`,
		}},
		{version: 3, statements: []string{
			`ALTER TABLE audit_outbox
				ADD COLUMN next_attempt_at TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE audit_outbox
				ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE audit_outbox
				ADD COLUMN lease_until TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE audit_outbox
				ADD COLUMN dead_lettered_at TEXT NOT NULL DEFAULT ''`,
			`DROP INDEX IF EXISTS audit_outbox_pending`,
			`CREATE INDEX audit_outbox_ready
				ON audit_outbox(
					tenant_id, delivered_at, dead_lettered_at,
					next_attempt_at, lease_until, created_at, id
				)`,
		}},
		{version: 4, statements: []string{
			`CREATE TABLE IF NOT EXISTS storage_settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL
			)`,
			`ALTER TABLE demonstrations
				ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE approvals
				ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX demonstrations_retention
				ON demonstrations(tenant_id, status, updated_at)`,
			`CREATE INDEX approvals_retention
				ON approvals(tenant_id, status, updated_at)`,
		}},
		{version: 5, statements: []string{
			`ALTER TABLE workflow_executions
				ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE workflow_executions
				ADD COLUMN lease_until TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE workflow_executions
				ADD COLUMN lease_token INTEGER NOT NULL DEFAULT 0`,
			`CREATE INDEX workflow_executions_lease
				ON workflow_executions(
					tenant_id, status, lease_until, updated_at, id
				)`,
		}},
	}
	return applyMigrations(ctx, s.db, migrations)
}

func (s *Store) configureProtection(
	ctx context.Context,
	options Options,
) error {
	var mode string
	err := s.db.QueryRowContext(
		ctx, `SELECT value FROM storage_settings
			WHERE key = 'document_protection'`,
	).Scan(&mode)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if mode == "required" && options.Protector == nil {
		return core.NewConfigError(
			"workflow sqlite database requires its configured document protector",
		)
	}
	if options.Protector == nil {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO storage_settings(
		key, value
	) VALUES ('document_protection', 'required')`)
	return err
}

type sqliteMigration struct {
	version    int
	statements []string
}

func applyMigrations(
	ctx context.Context,
	db *sql.DB,
	migrations []sqliteMigration,
) error {
	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("read workflow sqlite schema version: %w", err)
	}
	if current > CurrentSchemaVersion {
		return core.NewConfigError(fmt.Sprintf(
			"workflow sqlite schema version %d is newer than supported version %d",
			current, CurrentSchemaVersion,
		))
	}
	for _, migration := range migrations {
		if migration.version <= current {
			continue
		}
		if migration.version != current+1 {
			return core.NewConfigError(fmt.Sprintf(
				"workflow sqlite migration gap after version %d",
				current,
			))
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf(
				"begin workflow sqlite migration %d: %w",
				migration.version, err,
			)
		}
		for _, statement := range migration.statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf(
					"apply workflow sqlite migration %d: %w",
					migration.version, err,
				)
			}
		}
		if _, err := tx.ExecContext(
			ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.version),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf(
				"record workflow sqlite migration %d: %w",
				migration.version, err,
			)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf(
				"commit workflow sqlite migration %d: %w",
				migration.version, err,
			)
		}
		current = migration.version
	}
	if current != CurrentSchemaVersion {
		return core.NewConfigError(fmt.Sprintf(
			"workflow sqlite migrations ended at version %d; expected %d",
			current, CurrentSchemaVersion,
		))
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Workflows() workflow.Store {
	return workflowStore{db: s.db, documents: s.documents}
}
func (s *Store) Executions() workflow.ExecutionStore {
	return executionStore{db: s.db, documents: s.documents}
}
func (s *Store) LeasedExecutions() workflow.ExecutionLeaseStore {
	return executionStore{db: s.db, documents: s.documents}
}
func (s *Store) Demonstrations() observation.Store {
	return demonstrationStore{db: s.db, documents: s.documents}
}
func (s *Store) Approvals() policy.ApprovalStore {
	return approvalStore{db: s.db, documents: s.documents}
}
func (s *Store) ApprovalLifecycle() policy.ApprovalLifecycleStore {
	return approvalStore{db: s.db, documents: s.documents}
}
func (s *Store) AuthorizedApprovals(
	authorizer policy.ApprovalAuthorizer,
) (*policy.AuthorizedApprovalStore, error) {
	return policy.NewAuthorizedApprovalStore(
		s.ApprovalLifecycle(), authorizer,
	)
}
func (s *Store) Audit() audit.Store {
	return auditStore{db: s.db, documents: s.documents}
}
func (s *Store) AuditOutbox() audit.Outbox {
	return auditOutbox{db: s.db, documents: s.documents}
}
func (s *Store) LeasedAuditOutbox() audit.LeasedOutbox {
	return auditOutbox{db: s.db, documents: s.documents}
}
func (s *Store) Evaluations() evaluation.Store {
	return evaluationStore{db: s.db, documents: s.documents}
}
func (s *Store) Reviews() workflow.ReviewStore {
	return reviewStore{db: s.db, documents: s.documents}
}
func (s *Store) AgentEvaluations() evaluation.AgentStore {
	return agentEvaluationStore{db: s.db, documents: s.documents}
}
func (s *Store) ExtractorEvaluations() evaluation.ExtractorStore {
	return extractorEvaluationStore{db: s.db, documents: s.documents}
}
func (s *Store) Routes() workflow.RouteStore { return routeStore{s.db} }
func (s *Store) Feedback() workflow.FeedbackStore {
	return feedbackStore{db: s.db, documents: s.documents}
}

type workflowStore struct {
	db        *sql.DB
	documents documentCodec
}

type reviewStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s reviewStore) Save(ctx context.Context, review workflow.Review) error {
	if err := review.Validate(); err != nil {
		return err
	}
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID != review.TenantID || principal.ActorID != review.ReviewedBy {
		return core.NewPermissionError("workflow review identity does not match authenticated reviewer")
	}
	raw, err := s.documents.marshal(ctx, review)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_reviews(
		id, tenant_id, workflow_id, workflow_version, reviewed_at, document_json
	) VALUES (?, ?, ?, ?, ?, ?)`,
		review.ID, review.TenantID, review.WorkflowID, review.WorkflowVersion,
		review.ReviewedAt.Format(time.RFC3339Nano), raw,
	)
	if err != nil {
		return &core.SkawldError{
			Kind: core.ErrorConflict, Message: "save workflow review", Cause: err,
		}
	}
	return nil
}

func (s reviewStore) Get(
	ctx context.Context,
	reviewID string,
) (workflow.Review, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(
		ctx, `SELECT document_json FROM workflow_reviews WHERE id = ?`, reviewID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return workflow.Review{}, false, nil
	}
	if err != nil {
		return workflow.Review{}, false, err
	}
	var review workflow.Review
	if err := s.documents.unmarshal(ctx, raw, &review); err != nil {
		return workflow.Review{}, false, err
	}
	if !tenantAllowed(ctx, review.TenantID) {
		return workflow.Review{}, false, core.NewPermissionError("workflow review belongs to another tenant")
	}
	return review, true, nil
}

func (s reviewStore) List(
	ctx context.Context,
	workflowID string,
	version int,
) ([]workflow.Review, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return nil, core.NewPermissionError("workflow review storage requires an authenticated tenant")
	}
	query := `SELECT document_json FROM workflow_reviews WHERE tenant_id = ?`
	args := []interface{}{principal.TenantID}
	if workflowID != "" {
		query += ` AND workflow_id = ?`
		args = append(args, workflowID)
	}
	if version > 0 {
		query += ` AND workflow_version = ?`
		args = append(args, version)
	}
	query += ` ORDER BY reviewed_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]workflow.Review, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var review workflow.Review
		if err := s.documents.unmarshal(ctx, raw, &review); err != nil {
			return nil, err
		}
		output = append(output, review)
	}
	return output, rows.Err()
}

func (s workflowStore) SaveCandidate(ctx context.Context, version workflow.Version) (workflow.Version, error) {
	principal, _ := core.PrincipalFromContext(ctx)
	if version.Workflow.TenantID == "" {
		version.Workflow.TenantID = principal.TenantID
	}
	if !tenantAllowed(ctx, version.Workflow.TenantID) {
		return workflow.Version{}, core.NewPermissionError("workflow belongs to another tenant")
	}
	version.Status = workflow.VersionCandidate
	if version.SchemaVersion == "" {
		version.SchemaVersion = workflow.SchemaVersion
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now().UTC()
	}
	if err := version.Validate(); err != nil {
		return workflow.Version{}, err
	}
	raw, err := s.documents.marshal(ctx, version)
	if err != nil {
		return workflow.Version{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_versions(workflow_id, version, tenant_id, status, document_json)
		VALUES (?, ?, ?, ?, ?)`, version.Workflow.ID, version.Version, version.Workflow.TenantID, version.Status, raw)
	if err != nil {
		return workflow.Version{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "save workflow candidate", Cause: err}
	}
	return version, nil
}

func (s workflowStore) Publish(ctx context.Context, workflowID string, number int, principal core.Principal) (workflow.Version, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workflow.Version{}, err
	}
	defer tx.Rollback()
	version, status, ok, err := loadWorkflow(
		ctx, s.documents, tx, workflowID, number,
	)
	if err != nil {
		return workflow.Version{}, err
	}
	if !ok {
		return workflow.Version{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: "workflow version not found"}
	}
	if !tenantAllowed(ctx, version.Workflow.TenantID) || (version.Workflow.TenantID != "" && version.Workflow.TenantID != principal.TenantID) {
		return workflow.Version{}, core.NewPermissionError("workflow belongs to another tenant")
	}
	if status != workflow.VersionCandidate {
		return workflow.Version{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "only candidate workflows can be published"}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workflow_versions SET status = ? WHERE workflow_id = ? AND status = ?`,
		workflow.VersionRetired, workflowID, workflow.VersionPublished); err != nil {
		return workflow.Version{}, err
	}
	version.Status = workflow.VersionPublished
	version.PublishedAt = time.Now().UTC()
	version.PublishedBy = principal.ActorID
	raw, err := s.documents.marshal(ctx, version)
	if err != nil {
		return workflow.Version{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workflow_versions SET status = ?, document_json = ? WHERE workflow_id = ? AND version = ?`,
		workflow.VersionPublished, raw, workflowID, number); err != nil {
		return workflow.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return workflow.Version{}, err
	}
	return version, nil
}

func (s workflowStore) Get(ctx context.Context, workflowID string, number int) (workflow.Version, bool, error) {
	version, status, ok, err := loadWorkflow(
		ctx, s.documents, s.db, workflowID, number,
	)
	if err != nil || !ok {
		return version, ok, err
	}
	if !tenantAllowed(ctx, version.Workflow.TenantID) {
		return workflow.Version{}, false, core.NewPermissionError("workflow belongs to another tenant")
	}
	version.Status = status
	return version, true, nil
}

func (s workflowStore) Published(ctx context.Context, workflowID string) (workflow.Version, bool, error) {
	var number int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM workflow_versions WHERE workflow_id = ? AND status = ? ORDER BY version DESC LIMIT 1`,
		workflowID, workflow.VersionPublished).Scan(&number)
	if err == sql.ErrNoRows {
		return workflow.Version{}, false, nil
	}
	if err != nil {
		return workflow.Version{}, false, err
	}
	return s.Get(ctx, workflowID, number)
}

func (s workflowStore) ListVersions(ctx context.Context, workflowID string) ([]workflow.Version, error) {
	principal, err := storageTenant(ctx, "workflow")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(
		ctx, `SELECT version, status, document_json
			FROM workflow_versions
			WHERE workflow_id = ? AND tenant_id = ?
			ORDER BY version`,
		workflowID, principal.TenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var output []workflow.Version
	for rows.Next() {
		var number int
		var status workflow.VersionStatus
		var raw []byte
		if err := rows.Scan(&number, &status, &raw); err != nil {
			return nil, err
		}
		var version workflow.Version
		if err := s.documents.unmarshal(ctx, raw, &version); err != nil {
			return nil, err
		}
		if !tenantAllowed(ctx, version.Workflow.TenantID) {
			continue
		}
		version.Version = number
		version.Status = status
		output = append(output, version)
	}
	return output, rows.Err()
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func loadWorkflow(
	ctx context.Context,
	documents documentCodec,
	db queryRower,
	workflowID string,
	number int,
) (workflow.Version, workflow.VersionStatus, bool, error) {
	var raw []byte
	var status workflow.VersionStatus
	err := db.QueryRowContext(ctx, `SELECT status, document_json FROM workflow_versions WHERE workflow_id = ? AND version = ?`,
		workflowID, number).Scan(&status, &raw)
	if err == sql.ErrNoRows {
		return workflow.Version{}, "", false, nil
	}
	if err != nil {
		return workflow.Version{}, "", false, err
	}
	var version workflow.Version
	if err := documents.unmarshal(ctx, raw, &version); err != nil {
		return workflow.Version{}, "", false, err
	}
	version.Status = status
	return version, status, true, nil
}

type executionStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s executionStore) Create(
	ctx context.Context,
	execution workflow.Execution,
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_executions(
		id, tenant_id, workflow_id, workflow_version, status, revision, updated_at, document_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		execution.ID, execution.Principal.TenantID, execution.WorkflowID,
		execution.WorkflowVersion, execution.Status, execution.Revision,
		execution.UpdatedAt.Format(time.RFC3339Nano), raw,
	)
	if err != nil {
		return workflow.Execution{}, &core.SkawldError{
			Kind: core.ErrorConflict, Message: "create workflow execution", Cause: err,
		}
	}
	return execution, nil
}

func (s executionStore) Update(
	ctx context.Context,
	execution workflow.Execution,
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
			Kind: core.ErrorNotFound, Message: "workflow execution not found",
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
		leaseUntil, parseErr := time.Parse(time.RFC3339Nano, leaseUntilRaw)
		if parseErr != nil {
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
	if err := workflow.ValidateExecutionUpdate(current, execution); err != nil {
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
	result, err := tx.ExecContext(ctx, `UPDATE workflow_executions
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
			Kind: core.ErrorConflict, Message: "workflow execution revision conflict",
		}
	}
	if err := tx.Commit(); err != nil {
		return workflow.Execution{}, err
	}
	return execution, nil
}

func (s executionStore) Get(
	ctx context.Context,
	executionID string,
) (workflow.Execution, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(
		ctx,
		`SELECT document_json FROM workflow_executions WHERE id = ?`,
		executionID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return workflow.Execution{}, false, nil
	}
	if err != nil {
		return workflow.Execution{}, false, err
	}
	var execution workflow.Execution
	if err := s.documents.unmarshal(ctx, raw, &execution); err != nil {
		return workflow.Execution{}, false, err
	}
	if !tenantAllowed(ctx, execution.Principal.TenantID) {
		return workflow.Execution{}, false, core.NewPermissionError(
			"workflow execution belongs to another tenant",
		)
	}
	return execution, true, nil
}

func (s executionStore) List(
	ctx context.Context,
	filter workflow.ExecutionFilter,
) ([]workflow.Execution, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return nil, core.NewPermissionError(
			"workflow execution storage requires an authenticated tenant",
		)
	}
	if filter.Limit < 0 || filter.Limit > 1000 {
		return nil, core.NewConfigError(
			"workflow execution list limit must be between 0 and 1000",
		)
	}
	switch filter.Status {
	case "", workflow.ExecutionRunning, workflow.ExecutionAwaitingApproval,
		workflow.ExecutionRecoveryRequired, workflow.ExecutionCompleted,
		workflow.ExecutionFailed, workflow.ExecutionCanceled:
	default:
		return nil, core.NewConfigError("workflow execution list status is invalid")
	}
	query := `SELECT document_json FROM workflow_executions WHERE tenant_id = ?`
	args := []interface{}{principal.TenantID}
	if filter.WorkflowID != "" {
		query += ` AND workflow_id = ?`
		args = append(args, filter.WorkflowID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY updated_at DESC, id`
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	query += ` LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]workflow.Execution, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var execution workflow.Execution
		if err := s.documents.unmarshal(ctx, raw, &execution); err != nil {
			return nil, err
		}
		output = append(output, execution)
	}
	return output, rows.Err()
}

type demonstrationStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s demonstrationStore) Create(ctx context.Context, demo observation.Demonstration) error {
	if !tenantAllowed(ctx, demo.Principal.TenantID) {
		return core.NewPermissionError("demonstration belongs to another tenant")
	}
	raw, err := s.documents.marshal(ctx, demo)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO demonstrations(
		id, workflow_key, tenant_id, status, updated_at, document_json
	) VALUES (?, ?, ?, ?, ?, ?)`,
		demo.ID, demo.WorkflowKey, demo.Principal.TenantID, demo.Status,
		demo.StartedAt.UTC().Format(time.RFC3339Nano), raw)
	if err != nil {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "create demonstration", Cause: err}
	}
	return nil
}

func (s demonstrationStore) Append(ctx context.Context, demonstrationID string, event observation.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	demo, ok, err := loadDemonstration(
		ctx, s.documents, tx, demonstrationID,
	)
	if err != nil || !ok {
		if !ok && err == nil {
			err = &core.SkawldError{Kind: core.ErrorNotFound, Message: "demonstration not found"}
		}
		return err
	}
	if !tenantAllowed(ctx, demo.Principal.TenantID) {
		return core.NewPermissionError("demonstration belongs to another tenant")
	}
	if demo.Status != observation.DemonstrationRecording {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "demonstration is not recording"}
	}
	if err := observation.ValidateAppend(demo.Trace, event); err != nil {
		return err
	}
	demo.Trace.Events = append(demo.Trace.Events, event)
	if err := updateDemonstration(
		ctx, s.documents, tx, demo,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s demonstrationStore) Complete(ctx context.Context, demonstrationID string, result map[string]interface{}) (observation.Demonstration, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return observation.Demonstration{}, err
	}
	defer tx.Rollback()
	demo, ok, err := loadDemonstration(
		ctx, s.documents, tx, demonstrationID,
	)
	if err != nil || !ok {
		if !ok && err == nil {
			err = &core.SkawldError{Kind: core.ErrorNotFound, Message: "demonstration not found"}
		}
		return observation.Demonstration{}, err
	}
	if !tenantAllowed(ctx, demo.Principal.TenantID) {
		return observation.Demonstration{}, core.NewPermissionError("demonstration belongs to another tenant")
	}
	if demo.Status != observation.DemonstrationRecording {
		return observation.Demonstration{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "demonstration is not recording"}
	}
	demo.Status = observation.DemonstrationCompleted
	demo.CompletedAt = time.Now().UTC()
	demo.Trace.FinalResult = result
	if err := demo.Trace.Validate(); err != nil {
		return observation.Demonstration{}, err
	}
	if err := updateDemonstration(
		ctx, s.documents, tx, demo,
	); err != nil {
		return observation.Demonstration{}, err
	}
	if err := tx.Commit(); err != nil {
		return observation.Demonstration{}, err
	}
	return demo, nil
}

func (s demonstrationStore) Get(ctx context.Context, demonstrationID string) (observation.Demonstration, bool, error) {
	demo, ok, err := loadDemonstration(
		ctx, s.documents, s.db, demonstrationID,
	)
	if err != nil || !ok {
		return demo, ok, err
	}
	if !tenantAllowed(ctx, demo.Principal.TenantID) {
		return observation.Demonstration{}, false, core.NewPermissionError("demonstration belongs to another tenant")
	}
	return demo, true, nil
}

func (s demonstrationStore) List(ctx context.Context, workflowKey string) ([]observation.Demonstration, error) {
	principal, err := storageTenant(ctx, "demonstration")
	if err != nil {
		return nil, err
	}
	query := `SELECT document_json FROM demonstrations WHERE tenant_id = ?`
	args := []interface{}{principal.TenantID}
	if workflowKey != "" {
		query += ` AND workflow_key = ?`
		args = append(args, workflowKey)
	}
	query += ` ORDER BY id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var output []observation.Demonstration
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var demo observation.Demonstration
		if err := s.documents.unmarshal(ctx, raw, &demo); err != nil {
			return nil, err
		}
		if tenantAllowed(ctx, demo.Principal.TenantID) {
			output = append(output, demo)
		}
	}
	return output, rows.Err()
}

func loadDemonstration(
	ctx context.Context,
	documents documentCodec,
	db queryRower,
	demonstrationID string,
) (observation.Demonstration, bool, error) {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT document_json FROM demonstrations WHERE id = ?`, demonstrationID).Scan(&raw)
	if err == sql.ErrNoRows {
		return observation.Demonstration{}, false, nil
	}
	if err != nil {
		return observation.Demonstration{}, false, err
	}
	var demo observation.Demonstration
	if err := documents.unmarshal(ctx, raw, &demo); err != nil {
		return observation.Demonstration{}, false, err
	}
	return demo, true, nil
}

type execer interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func updateDemonstration(
	ctx context.Context,
	documents documentCodec,
	db execer,
	demo observation.Demonstration,
) error {
	raw, err := documents.marshal(ctx, demo)
	if err != nil {
		return err
	}
	updatedAt := demo.CompletedAt
	if updatedAt.IsZero() {
		if count := len(demo.Trace.Events); count > 0 {
			updatedAt = demo.Trace.Events[count-1].Timestamp
		} else {
			updatedAt = demo.StartedAt
		}
	}
	_, err = db.ExecContext(ctx, `UPDATE demonstrations
		SET status = ?, updated_at = ?, document_json = ?
		WHERE id = ?`,
		demo.Status, updatedAt.UTC().Format(time.RFC3339Nano), raw,
		demo.ID)
	return err
}

type approvalStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s approvalStore) Request(ctx context.Context, approval policy.Approval) (policy.Approval, error) {
	principal, err := storageActor(ctx, "approval")
	if err != nil {
		return policy.Approval{}, err
	}
	if approval.TenantID != "" && approval.TenantID != principal.TenantID {
		return policy.Approval{}, core.NewPermissionError("approval belongs to another tenant")
	}
	approval.TenantID = principal.TenantID
	if approval.ID == "" {
		approval.ID = id.New()
	}
	if approval.Status == "" {
		approval.Status = policy.ApprovalPending
	}
	if approval.Status != policy.ApprovalPending {
		return policy.Approval{}, core.NewConfigError(
			"new approval status must be pending",
		)
	}
	if approval.RequestedBy != "" &&
		approval.RequestedBy != principal.ActorID {
		return policy.Approval{}, core.NewPermissionError(
			"approval requester does not match authenticated actor",
		)
	}
	approval.RequestedBy = principal.ActorID
	if approval.RequestedAt.IsZero() {
		approval.RequestedAt = time.Now().UTC()
	}
	if !approval.ExpiresAt.IsZero() &&
		!approval.ExpiresAt.After(approval.RequestedAt) {
		return policy.Approval{}, core.NewConfigError(
			"approval expiration must be after its request time",
		)
	}
	raw, err := s.documents.marshal(ctx, approval)
	if err != nil {
		return policy.Approval{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO approvals(
		id, tenant_id, execution_id, status, updated_at, document_json
	) VALUES (?, ?, ?, ?, ?, ?)`,
		approval.ID, approval.TenantID, approval.ExecutionID,
		approval.Status, approval.RequestedAt.UTC().Format(time.RFC3339Nano),
		raw)
	if err != nil {
		return policy.Approval{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "create approval", Cause: err}
	}
	return approval, nil
}

func (s approvalStore) Get(ctx context.Context, approvalID string) (policy.Approval, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT document_json FROM approvals WHERE id = ?`, approvalID).Scan(&raw)
	if err == sql.ErrNoRows {
		return policy.Approval{}, false, nil
	}
	if err != nil {
		return policy.Approval{}, false, err
	}
	var approval policy.Approval
	if err := s.documents.unmarshal(ctx, raw, &approval); err != nil {
		return policy.Approval{}, false, err
	}
	if !tenantAllowed(ctx, approval.TenantID) {
		return policy.Approval{}, false, core.NewPermissionError("approval belongs to another tenant")
	}
	return approval, true, nil
}

func (s approvalStore) Decide(ctx context.Context, approvalID string, status policy.ApprovalStatus, principal core.Principal, reason string) (policy.Approval, error) {
	if status != policy.ApprovalGranted && status != policy.ApprovalRejected {
		return policy.Approval{}, core.NewConfigError("approval decision must be granted or rejected")
	}
	return s.transition(ctx, approvalID, status, principal, reason, false)
}

func (s approvalStore) Expire(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (policy.Approval, error) {
	return s.transition(
		ctx, approvalID, policy.ApprovalExpired, principal, reason, true,
	)
}

func (s approvalStore) Cancel(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (policy.Approval, error) {
	return s.transition(
		ctx, approvalID, policy.ApprovalCanceled, principal, reason, false,
	)
}

func (s approvalStore) transition(
	ctx context.Context,
	approvalID string,
	status policy.ApprovalStatus,
	principal core.Principal,
	reason string,
	requireDue bool,
) (policy.Approval, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > 4096 || strings.ContainsRune(reason, '\x00') {
		return policy.Approval{}, core.NewConfigError(
			"approval reason exceeds its safe bounds",
		)
	}
	authenticated, err := storageActor(ctx, "approval")
	if err != nil {
		return policy.Approval{}, err
	}
	if !principal.Authenticated() ||
		authenticated.TenantID != principal.TenantID ||
		authenticated.ActorID != principal.ActorID {
		return policy.Approval{}, core.NewPermissionError(
			"approval action requires the authenticated actor identity",
		)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return policy.Approval{}, err
	}
	defer tx.Rollback()
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT document_json FROM approvals WHERE id = ?`, approvalID).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return policy.Approval{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: "approval not found"}
		}
		return policy.Approval{}, err
	}
	var approval policy.Approval
	if err := s.documents.unmarshal(ctx, raw, &approval); err != nil {
		return policy.Approval{}, err
	}
	if approval.TenantID != "" && approval.TenantID != principal.TenantID {
		return policy.Approval{}, core.NewPermissionError("approval belongs to another tenant")
	}
	if approval.Status != policy.ApprovalPending {
		return policy.Approval{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "approval is already decided"}
	}
	now := time.Now().UTC()
	if requireDue &&
		(approval.ExpiresAt.IsZero() || now.Before(approval.ExpiresAt)) {
		return policy.Approval{}, &core.SkawldError{
			Kind: core.ErrorConflict, Message: "approval is not due to expire",
		}
	}
	approval.Status = status
	approval.DecidedAt = now
	approval.DecidedBy = principal.ActorID
	approval.Reason = reason
	raw, err = s.documents.marshal(ctx, approval)
	if err != nil {
		return policy.Approval{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE approvals
		SET status = ?, updated_at = ?, document_json = ?
		WHERE id = ?`,
		status, now.Format(time.RFC3339Nano), raw, approvalID,
	); err != nil {
		return policy.Approval{}, err
	}
	if err := tx.Commit(); err != nil {
		return policy.Approval{}, err
	}
	return approval, nil
}

func (s approvalStore) List(
	ctx context.Context,
	filter policy.ApprovalFilter,
) ([]policy.Approval, error) {
	principal, err := storageActor(ctx, "approval")
	if err != nil {
		return nil, err
	}
	if filter.Limit < 0 || filter.Limit > 1000 {
		return nil, core.NewConfigError(
			"approval list limit must be between 0 and 1000",
		)
	}
	switch filter.Status {
	case "", policy.ApprovalPending, policy.ApprovalGranted,
		policy.ApprovalRejected, policy.ApprovalExpired,
		policy.ApprovalCanceled:
	default:
		return nil, core.NewConfigError("approval list status is invalid")
	}
	query := `SELECT document_json FROM approvals WHERE tenant_id = ?`
	args := []interface{}{principal.TenantID}
	if filter.ExecutionID != "" {
		query += ` AND execution_id = ?`
		args = append(args, filter.ExecutionID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]policy.Approval, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var approval policy.Approval
		if err := s.documents.unmarshal(ctx, raw, &approval); err != nil {
			return nil, err
		}
		output = append(output, approval)
	}
	return output, rows.Err()
}

type auditStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s auditStore) Append(ctx context.Context, event audit.Event) error {
	if !tenantAllowed(ctx, event.TenantID) {
		return core.NewPermissionError("audit event belongs to another tenant")
	}
	if event.ID == "" {
		event.ID = id.New()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	raw, err := s.documents.marshal(ctx, event)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO audit_events(id, tenant_id, execution_id, timestamp, document_json) VALUES (?, ?, ?, ?, ?)`,
		event.ID, event.TenantID, event.ExecutionID, event.Timestamp.Format(time.RFC3339Nano), raw)
	return err
}

func (s auditStore) List(ctx context.Context, executionID string) ([]audit.Event, error) {
	principal, err := storageTenant(ctx, "audit")
	if err != nil {
		return nil, err
	}
	query := `SELECT document_json FROM audit_events WHERE tenant_id = ?`
	args := []interface{}{principal.TenantID}
	if executionID != "" {
		query += ` AND execution_id = ?`
		args = append(args, executionID)
	}
	query += ` ORDER BY seq`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var output []audit.Event
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event audit.Event
		if err := s.documents.unmarshal(ctx, raw, &event); err != nil {
			return nil, err
		}
		if tenantAllowed(ctx, event.TenantID) {
			output = append(output, event)
		}
	}
	return output, rows.Err()
}

type auditOutbox struct {
	db        *sql.DB
	documents documentCodec
}

func (s auditOutbox) Enqueue(ctx context.Context, event audit.Event) error {
	principal, err := storageActor(ctx, "audit outbox")
	if err != nil {
		return err
	}
	if event.ID == "" || event.Type == "" || event.Timestamp.IsZero() ||
		event.TenantID == "" {
		return core.NewConfigError(
			"audit outbox event requires id, type, timestamp, and tenant",
		)
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
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO audit_outbox(
		id, tenant_id, created_at, document_json
	) VALUES (?, ?, ?, ?)`,
		event.ID, event.TenantID, time.Now().UTC().Format(time.RFC3339Nano), raw,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected > 0 {
		return err
	}
	var tenantID string
	var existingRaw []byte
	if err := s.db.QueryRowContext(
		ctx, `SELECT tenant_id, document_json FROM audit_outbox WHERE id = ?`,
		event.ID,
	).Scan(&tenantID, &existingRaw); err != nil {
		return err
	}
	if tenantID != principal.TenantID {
		return core.NewPermissionError(
			"audit outbox event belongs to another tenant",
		)
	}
	var existing audit.Event
	if err := s.documents.unmarshal(
		ctx, existingRaw, &existing,
	); err != nil {
		return err
	}
	if !reflect.DeepEqual(existing, event) {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "audit outbox event id already has different content",
		}
	}
	return nil
}

func (s auditOutbox) Pending(
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
			"audit outbox pending limit must be between 1 and 1000",
		)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		document_json, attempts, created_at, last_attempt_at, last_error,
		delivered_at, next_attempt_at, lease_owner, lease_until,
		dead_lettered_at
		FROM audit_outbox
		WHERE tenant_id = ? AND delivered_at = '' AND dead_lettered_at = ''
		ORDER BY created_at, id
		LIMIT ?`, principal.TenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]audit.Delivery, 0)
	for rows.Next() {
		var raw []byte
		var attempts int
		var createdAt, lastAttemptAt, lastError, deliveredAt string
		var nextAttemptAt, leaseOwner, leaseUntil, deadLetteredAt string
		if err := rows.Scan(
			&raw, &attempts, &createdAt, &lastAttemptAt, &lastError,
			&deliveredAt, &nextAttemptAt, &leaseOwner, &leaseUntil,
			&deadLetteredAt,
		); err != nil {
			return nil, err
		}
		var event audit.Event
		if err := s.documents.unmarshal(ctx, raw, &event); err != nil {
			return nil, err
		}
		delivery := audit.Delivery{
			Event: event, Attempts: attempts, LastError: lastError,
			LeaseOwner: leaseOwner,
		}
		if delivery.CreatedAt, err = parseSQLiteTime(createdAt); err != nil {
			return nil, err
		}
		if lastAttemptAt != "" {
			if delivery.LastAttemptAt, err = parseSQLiteTime(
				lastAttemptAt,
			); err != nil {
				return nil, err
			}
		}
		if deliveredAt != "" {
			if delivery.DeliveredAt, err = parseSQLiteTime(
				deliveredAt,
			); err != nil {
				return nil, err
			}
		}
		if nextAttemptAt != "" {
			if delivery.NextAttemptAt, err = parseSQLiteTime(
				nextAttemptAt,
			); err != nil {
				return nil, err
			}
		}
		if leaseUntil != "" {
			if delivery.LeaseUntil, err = parseSQLiteTime(
				leaseUntil,
			); err != nil {
				return nil, err
			}
		}
		if deadLetteredAt != "" {
			if delivery.DeadLetteredAt, err = parseSQLiteTime(
				deadLetteredAt,
			); err != nil {
				return nil, err
			}
		}
		output = append(output, delivery)
	}
	return output, rows.Err()
}

func (s auditOutbox) MarkAttempt(
	ctx context.Context,
	eventID string,
	message string,
) error {
	return s.mark(ctx, eventID, false, message)
}

func (s auditOutbox) MarkDelivered(
	ctx context.Context,
	eventID string,
) error {
	return s.mark(ctx, eventID, true, "")
}

func (s auditOutbox) mark(
	ctx context.Context,
	eventID string,
	delivered bool,
	message string,
) error {
	principal, err := storageActor(ctx, "audit outbox")
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	deliveredAt := ""
	if delivered {
		deliveredAt = now
		message = ""
	}
	message = strings.TrimSpace(message)
	if len(message) > 1024 {
		message = message[:1024]
	}
	result, err := s.db.ExecContext(ctx, `UPDATE audit_outbox
		SET attempts = attempts + 1, last_attempt_at = ?,
			last_error = ?, delivered_at = ?
		WHERE id = ? AND tenant_id = ? AND delivered_at = ''`,
		now, message, deliveredAt, eventID, principal.TenantID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var count int
		if err := s.db.QueryRowContext(
			ctx, `SELECT COUNT(*) FROM audit_outbox
				WHERE id = ? AND tenant_id = ?`,
			eventID, principal.TenantID,
		).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return &core.SkawldError{
				Kind:    core.ErrorNotFound,
				Message: "audit outbox event not found",
			}
		}
	}
	return nil
}

func parseSQLiteTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"parse workflow sqlite timestamp: %w", err,
		)
	}
	return parsed, nil
}

type evaluationStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s evaluationStore) Save(ctx context.Context, report evaluation.Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return core.NewPermissionError("evaluation report belongs to another tenant")
	}
	raw, err := s.documents.marshal(ctx, report)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO evaluation_reports(
		id, tenant_id, workflow_id, workflow_version, started_at, document_json
	) VALUES (?, ?, ?, ?, ?, ?)`,
		report.ID, report.TenantID, report.WorkflowID, report.WorkflowVersion,
		report.StartedAt.Format(time.RFC3339Nano), raw,
	)
	if err != nil {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "save evaluation report", Cause: err}
	}
	return nil
}

func (s evaluationStore) Get(ctx context.Context, reportID string) (evaluation.Report, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT document_json FROM evaluation_reports WHERE id = ?`, reportID).Scan(&raw)
	if err == sql.ErrNoRows {
		return evaluation.Report{}, false, nil
	}
	if err != nil {
		return evaluation.Report{}, false, err
	}
	var report evaluation.Report
	if err := s.documents.unmarshal(ctx, raw, &report); err != nil {
		return evaluation.Report{}, false, err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return evaluation.Report{}, false, core.NewPermissionError("evaluation report belongs to another tenant")
	}
	return report, true, nil
}

func (s evaluationStore) List(ctx context.Context, workflowID string, version int) ([]evaluation.Report, error) {
	query := `SELECT document_json FROM evaluation_reports`
	args := []interface{}{}
	conditions := make([]string, 0, 2)
	if workflowID != "" {
		conditions = append(conditions, "workflow_id = ?")
		args = append(args, workflowID)
	}
	if version > 0 {
		conditions = append(conditions, "workflow_version = ?")
		args = append(args, version)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += ` ORDER BY started_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]evaluation.Report, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var report evaluation.Report
		if err := s.documents.unmarshal(ctx, raw, &report); err != nil {
			return nil, err
		}
		if tenantAllowed(ctx, report.TenantID) {
			output = append(output, report)
		}
	}
	return output, rows.Err()
}

type agentEvaluationStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s agentEvaluationStore) SaveAgentReport(ctx context.Context, report evaluation.AgentReport) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return core.NewPermissionError("agent evaluation report belongs to another tenant")
	}
	raw, err := s.documents.marshal(ctx, report)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_evaluation_reports(
		id, tenant_id, suite_name, started_at, document_json
	) VALUES (?, ?, ?, ?, ?)`,
		report.ID, report.TenantID, report.SuiteName, report.StartedAt.Format(time.RFC3339Nano), raw,
	)
	if err != nil {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "save agent evaluation report", Cause: err}
	}
	return nil
}

func (s agentEvaluationStore) GetAgentReport(
	ctx context.Context,
	reportID string,
) (evaluation.AgentReport, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT document_json FROM agent_evaluation_reports WHERE id = ?`, reportID).Scan(&raw)
	if err == sql.ErrNoRows {
		return evaluation.AgentReport{}, false, nil
	}
	if err != nil {
		return evaluation.AgentReport{}, false, err
	}
	var report evaluation.AgentReport
	if err := s.documents.unmarshal(ctx, raw, &report); err != nil {
		return evaluation.AgentReport{}, false, err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return evaluation.AgentReport{}, false, core.NewPermissionError("agent evaluation report belongs to another tenant")
	}
	return report, true, nil
}

func (s agentEvaluationStore) ListAgentReports(
	ctx context.Context,
	suiteName string,
) ([]evaluation.AgentReport, error) {
	query := `SELECT document_json FROM agent_evaluation_reports`
	args := []interface{}{}
	if suiteName != "" {
		query += ` WHERE suite_name = ?`
		args = append(args, suiteName)
	}
	query += ` ORDER BY started_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]evaluation.AgentReport, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var report evaluation.AgentReport
		if err := s.documents.unmarshal(ctx, raw, &report); err != nil {
			return nil, err
		}
		if tenantAllowed(ctx, report.TenantID) {
			output = append(output, report)
		}
	}
	return output, rows.Err()
}

type extractorEvaluationStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s extractorEvaluationStore) SaveExtractorReport(
	ctx context.Context,
	report evaluation.ExtractorReport,
) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return core.NewPermissionError("extractor evaluation report belongs to another tenant")
	}
	raw, err := s.documents.marshal(ctx, report)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO extractor_evaluation_reports(
		id, tenant_id, suite_name, started_at, document_json
	) VALUES (?, ?, ?, ?, ?)`,
		report.ID, report.TenantID, report.SuiteName, report.StartedAt.Format(time.RFC3339Nano), raw,
	)
	if err != nil {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "save extractor evaluation report", Cause: err}
	}
	return nil
}

func (s extractorEvaluationStore) GetExtractorReport(
	ctx context.Context,
	reportID string,
) (evaluation.ExtractorReport, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT document_json FROM extractor_evaluation_reports WHERE id = ?`, reportID).Scan(&raw)
	if err == sql.ErrNoRows {
		return evaluation.ExtractorReport{}, false, nil
	}
	if err != nil {
		return evaluation.ExtractorReport{}, false, err
	}
	var report evaluation.ExtractorReport
	if err := s.documents.unmarshal(ctx, raw, &report); err != nil {
		return evaluation.ExtractorReport{}, false, err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return evaluation.ExtractorReport{}, false, core.NewPermissionError("extractor evaluation report belongs to another tenant")
	}
	return report, true, nil
}

func (s extractorEvaluationStore) ListExtractorReports(
	ctx context.Context,
	suiteName string,
) ([]evaluation.ExtractorReport, error) {
	query := `SELECT document_json FROM extractor_evaluation_reports`
	args := []interface{}{}
	if suiteName != "" {
		query += ` WHERE suite_name = ?`
		args = append(args, suiteName)
	}
	query += ` ORDER BY started_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]evaluation.ExtractorReport, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var report evaluation.ExtractorReport
		if err := s.documents.unmarshal(ctx, raw, &report); err != nil {
			return nil, err
		}
		if tenantAllowed(ctx, report.TenantID) {
			output = append(output, report)
		}
	}
	return output, rows.Err()
}

type routeStore struct{ db *sql.DB }

func (s routeStore) Save(ctx context.Context, route workflow.Route) (workflow.Route, error) {
	principal, err := storageActor(ctx, "workflow route")
	if err != nil {
		return workflow.Route{}, err
	}
	route.TaskType = strings.TrimSpace(route.TaskType)
	route.WorkflowID = strings.TrimSpace(route.WorkflowID)
	if err := route.Validate(); err != nil {
		return workflow.Route{}, err
	}
	if route.TenantID != "" && route.TenantID != principal.TenantID {
		return workflow.Route{}, core.NewPermissionError("workflow route belongs to another tenant")
	}
	route.TenantID = principal.TenantID
	route.UpdatedAt = time.Now().UTC()
	route.UpdatedBy = principal.ActorID
	if route.Revision == 0 {
		route.Revision = 1
		_, err := s.db.ExecContext(ctx, `INSERT INTO workflow_routes(
			tenant_id, task_type, workflow_id, revision, updated_at, updated_by
		) VALUES (?, ?, ?, ?, ?, ?)`,
			route.TenantID, route.TaskType, route.WorkflowID, route.Revision,
			route.UpdatedAt.Format(time.RFC3339Nano), route.UpdatedBy,
		)
		if err != nil {
			return workflow.Route{}, &core.SkawldError{
				Kind: core.ErrorConflict, Message: "workflow route already exists", Cause: err,
			}
		}
		return route, nil
	}
	previousRevision := route.Revision
	route.Revision++
	result, err := s.db.ExecContext(ctx, `UPDATE workflow_routes
		SET workflow_id = ?, revision = ?, updated_at = ?, updated_by = ?
		WHERE tenant_id = ? AND task_type = ? AND revision = ?`,
		route.WorkflowID, route.Revision, route.UpdatedAt.Format(time.RFC3339Nano),
		route.UpdatedBy, route.TenantID, route.TaskType, previousRevision,
	)
	if err != nil {
		return workflow.Route{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return workflow.Route{}, err
	}
	if affected == 0 {
		exists, err := routeExists(ctx, s.db, route.TenantID, route.TaskType)
		if err != nil {
			return workflow.Route{}, err
		}
		kind, message := core.ErrorNotFound, "workflow route not found"
		if exists {
			kind, message = core.ErrorConflict, "workflow route revision conflict"
		}
		return workflow.Route{}, &core.SkawldError{Kind: kind, Message: message}
	}
	return route, nil
}

func (s routeStore) Get(
	ctx context.Context,
	taskType string,
) (workflow.Route, bool, error) {
	principal, err := storageActor(ctx, "workflow route")
	if err != nil {
		return workflow.Route{}, false, err
	}
	taskType = strings.TrimSpace(taskType)
	if taskType == "" || len(taskType) > 256 ||
		strings.ContainsAny(taskType, "\r\n\x00") {
		return workflow.Route{}, false, core.NewConfigError("workflow route task type is required")
	}
	var route workflow.Route
	var updatedAt string
	err = s.db.QueryRowContext(ctx, `SELECT
		workflow_id, revision, updated_at, updated_by
		FROM workflow_routes WHERE tenant_id = ? AND task_type = ?`,
		principal.TenantID, taskType,
	).Scan(&route.WorkflowID, &route.Revision, &updatedAt, &route.UpdatedBy)
	if err == sql.ErrNoRows {
		return workflow.Route{}, false, nil
	}
	if err != nil {
		return workflow.Route{}, false, err
	}
	route.TaskType = taskType
	route.TenantID = principal.TenantID
	route.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return workflow.Route{}, false, err
	}
	return route, true, nil
}

func (s routeStore) List(ctx context.Context) ([]workflow.Route, error) {
	principal, err := storageActor(ctx, "workflow route")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		task_type, workflow_id, revision, updated_at, updated_by
		FROM workflow_routes WHERE tenant_id = ? ORDER BY task_type`,
		principal.TenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]workflow.Route, 0)
	for rows.Next() {
		var route workflow.Route
		var updatedAt string
		if err := rows.Scan(
			&route.TaskType, &route.WorkflowID, &route.Revision,
			&updatedAt, &route.UpdatedBy,
		); err != nil {
			return nil, err
		}
		route.TenantID = principal.TenantID
		route.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, err
		}
		output = append(output, route)
	}
	return output, rows.Err()
}

func (s routeStore) Delete(
	ctx context.Context,
	taskType string,
	revision int64,
) error {
	principal, err := storageActor(ctx, "workflow route")
	if err != nil {
		return err
	}
	taskType = strings.TrimSpace(taskType)
	if taskType == "" || len(taskType) > 256 ||
		strings.ContainsAny(taskType, "\r\n\x00") || revision < 1 {
		return core.NewConfigError("workflow route deletion requires task type and revision")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM workflow_routes
		WHERE tenant_id = ? AND task_type = ? AND revision = ?`,
		principal.TenantID, taskType, revision,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		exists, err := routeExists(ctx, s.db, principal.TenantID, taskType)
		if err != nil {
			return err
		}
		kind, message := core.ErrorNotFound, "workflow route not found"
		if exists {
			kind, message = core.ErrorConflict, "workflow route revision conflict"
		}
		return &core.SkawldError{Kind: kind, Message: message}
	}
	return nil
}

func routeExists(
	ctx context.Context,
	db *sql.DB,
	tenantID, taskType string,
) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_routes
		WHERE tenant_id = ? AND task_type = ?`, tenantID, taskType,
	).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

type feedbackStore struct {
	db        *sql.DB
	documents documentCodec
}

func (s feedbackStore) Save(
	ctx context.Context,
	feedback workflow.ExecutionFeedback,
) error {
	if err := feedback.Validate(); err != nil {
		return err
	}
	principal, err := storageActor(ctx, "workflow feedback")
	if err != nil {
		return err
	}
	if principal.TenantID != feedback.TenantID ||
		principal.ActorID != feedback.CreatedBy {
		return core.NewPermissionError(
			"workflow feedback identity does not match authenticated actor",
		)
	}
	raw, err := s.documents.marshal(ctx, feedback)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_feedback(
		id, tenant_id, execution_id, workflow_id, workflow_version,
		disposition, created_at, document_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		feedback.ID, feedback.TenantID, feedback.ExecutionID,
		feedback.WorkflowID, feedback.WorkflowVersion, feedback.Disposition,
		feedback.CreatedAt.Format(time.RFC3339Nano), raw,
	)
	if err != nil {
		return &core.SkawldError{
			Kind: core.ErrorConflict, Message: "save workflow feedback", Cause: err,
		}
	}
	return nil
}

func (s feedbackStore) Get(
	ctx context.Context,
	feedbackID string,
) (workflow.ExecutionFeedback, bool, error) {
	principal, err := storageTenant(ctx, "workflow feedback")
	if err != nil {
		return workflow.ExecutionFeedback{}, false, err
	}
	var raw []byte
	err = s.db.QueryRowContext(ctx, `SELECT document_json FROM workflow_feedback
		WHERE id = ? AND tenant_id = ?`,
		feedbackID, principal.TenantID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return workflow.ExecutionFeedback{}, false, nil
	}
	if err != nil {
		return workflow.ExecutionFeedback{}, false, err
	}
	var feedback workflow.ExecutionFeedback
	if err := s.documents.unmarshal(ctx, raw, &feedback); err != nil {
		return workflow.ExecutionFeedback{}, false, err
	}
	return feedback, true, nil
}

func (s feedbackStore) List(
	ctx context.Context,
	filter workflow.FeedbackFilter,
) ([]workflow.ExecutionFeedback, error) {
	principal, err := storageTenant(ctx, "workflow feedback")
	if err != nil {
		return nil, err
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	query := `SELECT document_json FROM workflow_feedback WHERE tenant_id = ?`
	args := []interface{}{principal.TenantID}
	if filter.WorkflowID != "" {
		query += ` AND workflow_id = ?`
		args = append(args, filter.WorkflowID)
	}
	if filter.WorkflowVersion > 0 {
		query += ` AND workflow_version = ?`
		args = append(args, filter.WorkflowVersion)
	}
	if filter.ExecutionID != "" {
		query += ` AND execution_id = ?`
		args = append(args, filter.ExecutionID)
	}
	if filter.Disposition != "" {
		query += ` AND disposition = ?`
		args = append(args, filter.Disposition)
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	query += ` ORDER BY created_at DESC, id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]workflow.ExecutionFeedback, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var feedback workflow.ExecutionFeedback
		if err := s.documents.unmarshal(ctx, raw, &feedback); err != nil {
			return nil, err
		}
		output = append(output, feedback)
	}
	return output, rows.Err()
}

func storageActor(ctx context.Context, resource string) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" || principal.ActorID == "" {
		return core.Principal{}, core.NewPermissionError(
			resource + " storage requires an authenticated actor",
		)
	}
	return principal, nil
}

func storageTenant(ctx context.Context, resource string) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return core.Principal{}, core.NewPermissionError(
			resource + " storage requires an authenticated tenant",
		)
	}
	return principal, nil
}

func tenantAllowed(ctx context.Context, tenantID string) bool {
	if tenantID == "" {
		return true
	}
	principal, ok := core.PrincipalFromContext(ctx)
	return ok && principal.TenantID == tenantID
}

var (
	_ workflow.Store                = workflowStore{}
	_ workflow.ExecutionStore       = executionStore{}
	_ observation.Store             = demonstrationStore{}
	_ policy.ApprovalStore          = approvalStore{}
	_ policy.ApprovalLifecycleStore = approvalStore{}
	_ audit.Store                   = auditStore{}
	_ audit.Outbox                  = auditOutbox{}
	_ audit.LeasedOutbox            = auditOutbox{}
	_ evaluation.Store              = evaluationStore{}
	_ evaluation.AgentStore         = agentEvaluationStore{}
	_ evaluation.ExtractorStore     = extractorEvaluationStore{}
	_ workflow.RouteStore           = routeStore{}
	_ workflow.FeedbackStore        = feedbackStore{}
)
