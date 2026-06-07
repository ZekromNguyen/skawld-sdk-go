package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
	idgen "github.com/skawld/skawld-sdk-go/internal/id"
	_ "modernc.org/sqlite"
)

// Store persists sessions, messages, tasks, task edges, and invoked skills in
// a SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens or creates a SQLite session store at path.
func Open(path string) (*Store, error) {
	return OpenContext(context.Background(), path)
}

// OpenContext opens or creates a SQLite session store at path using ctx for
// initial connection and schema setup work.
func OpenContext(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite store path is required")
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
	if err := initializeSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func initializeSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			meta_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			session_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			appended_at TEXT NOT NULL,
			message_json TEXT NOT NULL,
			PRIMARY KEY (session_id, seq),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS invoked_skills (
			session_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			skill_json TEXT NOT NULL,
			PRIMARY KEY (session_id, position),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			session_id TEXT NOT NULL,
			id TEXT NOT NULL,
			subject TEXT NOT NULL,
			description TEXT NOT NULL,
			active_form TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			owner TEXT NOT NULL DEFAULT '',
			metadata_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (session_id, id),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS task_edges (
			session_id TEXT NOT NULL,
			blocker_id TEXT NOT NULL,
			blocked_id TEXT NOT NULL,
			PRIMARY KEY (session_id, blocker_id, blocked_id),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY (session_id, blocker_id) REFERENCES tasks(session_id, id) ON DELETE CASCADE,
			FOREIGN KEY (session_id, blocked_id) REFERENCES tasks(session_id, id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS task_counters (
			session_id TEXT PRIMARY KEY,
			last_id INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`PRAGMA user_version = 1`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Create(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	if id == "" {
		id = idgen.New()
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SessionRecord{}, err
	}
	defer rollback(tx)
	if rec, ok, err := loadSessionTx(ctx, tx, id); err != nil || ok {
		return rec, err
	}
	now := nowString()
	metaJSON, err := marshalJSON(meta)
	if err != nil {
		return core.SessionRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id, created_at, updated_at, meta_json) VALUES (?, ?, ?, ?)`, id, now, now, metaJSON); err != nil {
		return core.SessionRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.SessionRecord{}, err
	}
	return core.SessionRecord{ID: id, CreatedAt: now, UpdatedAt: now, Meta: meta}, nil
}

func (s *Store) Load(ctx context.Context, id string) (core.SessionRecord, bool, error) {
	rec, ok, err := loadSession(ctx, s.db, id)
	if err != nil || !ok {
		return rec, ok, err
	}
	skills, err := loadInvokedSkills(ctx, s.db, id)
	if err != nil {
		return core.SessionRecord{}, false, err
	}
	rec.InvokedSkills = skills
	return rec, true, nil
}

func (s *Store) LoadMessages(ctx context.Context, id string) ([]core.StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, appended_at, message_json FROM messages WHERE session_id = ? ORDER BY seq ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.StoredMessage
	for rows.Next() {
		var stored core.StoredMessage
		var raw string
		if err := rows.Scan(&stored.Seq, &stored.AppendedAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &stored.Message); err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, rows.Err()
}

func (s *Store) AppendMessages(ctx context.Context, id string, messages []core.Message) ([]core.StoredMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	now := nowString()
	if err := ensureSessionTx(ctx, tx, id, now); err != nil {
		return nil, err
	}
	var maxSeq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM messages WHERE session_id = ?`, id).Scan(&maxSeq); err != nil {
		return nil, err
	}
	appended := make([]core.StoredMessage, 0, len(messages))
	for i, msg := range messages {
		raw, err := marshalJSON(msg)
		if err != nil {
			return nil, err
		}
		stored := core.StoredMessage{Seq: maxSeq + i + 1, AppendedAt: now, Message: msg}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(session_id, seq, appended_at, message_json) VALUES (?, ?, ?, ?)`, id, stored.Seq, stored.AppendedAt, raw); err != nil {
			return nil, err
		}
		appended = append(appended, stored)
	}
	if err := touchSessionTx(ctx, tx, id, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return appended, nil
}

func (s *Store) UpdateMeta(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SessionRecord{}, err
	}
	defer rollback(tx)
	rec, ok, err := loadSessionTx(ctx, tx, id)
	if err != nil {
		return core.SessionRecord{}, err
	}
	if !ok {
		return core.SessionRecord{}, fmt.Errorf("session not found")
	}
	if rec.Meta == nil {
		rec.Meta = map[string]interface{}{}
	}
	for k, v := range meta {
		rec.Meta[k] = v
	}
	rec.UpdatedAt = nowString()
	metaJSON, err := marshalJSON(rec.Meta)
	if err != nil {
		return core.SessionRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET meta_json = ?, updated_at = ? WHERE id = ?`, metaJSON, rec.UpdatedAt, id); err != nil {
		return core.SessionRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.SessionRecord{}, err
	}
	return rec, nil
}

func (s *Store) SetInvokedSkills(ctx context.Context, id string, skills []core.InvokedSkillRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := ensureSessionTx(ctx, tx, id, nowString()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM invoked_skills WHERE session_id = ?`, id); err != nil {
		return err
	}
	for i, skill := range skills {
		raw, err := marshalJSON(skill)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO invoked_skills(session_id, position, skill_json) VALUES (?, ?, ?)`, id, i, raw); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) List(ctx context.Context, limit, offset int) ([]core.SessionRecord, error) {
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, created_at, updated_at, meta_json FROM sessions ORDER BY updated_at DESC`
	args := []interface{}{}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	} else if offset > 0 {
		query += ` LIMIT -1 OFFSET ?`
		args = append(args, offset)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.SessionRecord
	for rows.Next() {
		rec, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) CreateTask(ctx context.Context, sessionID string, input core.CreateTaskInput) (core.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Task{}, err
	}
	defer rollback(tx)
	now := nowString()
	if err := ensureSessionTx(ctx, tx, sessionID, now); err != nil {
		return core.Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_counters(session_id, last_id) VALUES (?, 0) ON CONFLICT(session_id) DO NOTHING`, sessionID); err != nil {
		return core.Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_counters SET last_id = last_id + 1 WHERE session_id = ?`, sessionID); err != nil {
		return core.Task{}, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT last_id FROM task_counters WHERE session_id = ?`, sessionID).Scan(&n); err != nil {
		return core.Task{}, err
	}
	task := core.Task{
		ID:          strconv.Itoa(n),
		SessionID:   sessionID,
		Subject:     input.Subject,
		Description: input.Description,
		ActiveForm:  input.ActiveForm,
		Status:      core.TaskPending,
		Blocks:      []string{},
		BlockedBy:   []string{},
		Metadata:    input.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := insertTaskTx(ctx, tx, task); err != nil {
		return core.Task{}, err
	}
	if err := touchSessionTx(ctx, tx, sessionID, now); err != nil {
		return core.Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.Task{}, err
	}
	return task, nil
}

func (s *Store) GetTask(ctx context.Context, sessionID, taskID string) (core.Task, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Task{}, false, err
	}
	defer rollback(tx)
	task, ok, err := loadTaskTx(ctx, tx, sessionID, taskID)
	if err != nil {
		return core.Task{}, false, err
	}
	if !ok {
		return core.Task{}, false, nil
	}
	if err := loadTaskEdgesTx(ctx, tx, sessionID, map[string]*core.Task{task.ID: &task}); err != nil {
		return core.Task{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return core.Task{}, false, err
	}
	return task, true, nil
}

func (s *Store) ListTasks(ctx context.Context, sessionID string) ([]core.Task, error) {
	tasks, err := loadTasks(ctx, s.db, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]core.Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aErr := strconv.Atoi(out[i].ID)
		aj, bErr := strconv.Atoi(out[j].ID)
		if aErr == nil && bErr == nil {
			return ai < aj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *Store) UpdateTask(ctx context.Context, sessionID, taskID string, patch core.TaskPatch) (core.Task, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Task{}, false, err
	}
	defer rollback(tx)
	task, ok, err := loadTaskTx(ctx, tx, sessionID, taskID)
	if err != nil {
		return core.Task{}, false, err
	}
	if !ok {
		return core.Task{}, false, nil
	}
	if err := loadTaskEdgesTx(ctx, tx, sessionID, map[string]*core.Task{task.ID: &task}); err != nil {
		return core.Task{}, false, err
	}
	if patch.Subject != nil {
		task.Subject = *patch.Subject
	}
	if patch.Description != nil {
		task.Description = *patch.Description
	}
	if patch.ActiveForm != nil {
		task.ActiveForm = *patch.ActiveForm
	}
	if patch.Status != nil {
		task.Status = *patch.Status
	}
	if patch.Delete {
		task.Status = core.TaskDeleted
	}
	if patch.Owner != nil {
		task.Owner = *patch.Owner
	}
	if patch.Metadata != nil {
		if task.Metadata == nil {
			task.Metadata = map[string]interface{}{}
		}
		for k, v := range patch.Metadata {
			if v == nil {
				delete(task.Metadata, k)
			} else {
				task.Metadata[k] = v
			}
		}
		if len(task.Metadata) == 0 {
			task.Metadata = nil
		}
	}
	if task.Status == core.TaskDeleted {
		task.Blocks = []string{}
		task.BlockedBy = []string{}
	} else if err := applyTaskEdgesTx(ctx, tx, sessionID, taskID, &task, patch); err != nil {
		return core.Task{}, true, err
	}
	task.UpdatedAt = nowString()
	if err := updateTaskRowTx(ctx, tx, task); err != nil {
		return core.Task{}, true, err
	}
	if err := applyTaskEdgeChangesTx(ctx, tx, sessionID, taskID, task, patch); err != nil {
		return core.Task{}, true, err
	}
	if task.Status != core.TaskDeleted {
		for _, blockedID := range task.Blocks {
			cyclic, err := taskPathExistsTx(ctx, tx, sessionID, blockedID, taskID)
			if err != nil {
				return core.Task{}, true, err
			}
			if cyclic {
				return core.Task{}, true, fmt.Errorf("task dependency cycle detected")
			}
		}
	}
	if err := touchSessionTx(ctx, tx, sessionID, task.UpdatedAt); err != nil {
		return core.Task{}, true, err
	}
	if err := tx.Commit(); err != nil {
		return core.Task{}, true, err
	}
	return task, true, nil
}

func (s *Store) DeleteTask(ctx context.Context, sessionID, taskID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	_, ok, err := loadTaskTx(ctx, tx, sessionID, taskID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_edges WHERE session_id = ? AND (blocker_id = ? OR blocked_id = ?)`, sessionID, taskID, taskID); err != nil {
		return true, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE session_id = ? AND id = ?`, sessionID, taskID); err != nil {
		return true, err
	}
	if err := touchSessionTx(ctx, tx, sessionID, nowString()); err != nil {
		return true, err
	}
	if err := tx.Commit(); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanSession(row rowScanner) (core.SessionRecord, error) {
	var rec core.SessionRecord
	var metaJSON string
	if err := row.Scan(&rec.ID, &rec.CreatedAt, &rec.UpdatedAt, &metaJSON); err != nil {
		return core.SessionRecord{}, err
	}
	if metaJSON == "" {
		rec.Meta = map[string]interface{}{}
		return rec, nil
	}
	if err := json.Unmarshal([]byte(metaJSON), &rec.Meta); err != nil {
		return core.SessionRecord{}, err
	}
	if rec.Meta == nil {
		rec.Meta = map[string]interface{}{}
	}
	return rec, nil
}

func loadSession(ctx context.Context, db *sql.DB, id string) (core.SessionRecord, bool, error) {
	return loadSessionRow(db.QueryRowContext(ctx, `SELECT id, created_at, updated_at, meta_json FROM sessions WHERE id = ?`, id))
}

func loadSessionTx(ctx context.Context, tx *sql.Tx, id string) (core.SessionRecord, bool, error) {
	return loadSessionRow(tx.QueryRowContext(ctx, `SELECT id, created_at, updated_at, meta_json FROM sessions WHERE id = ?`, id))
}

func loadSessionRow(row rowScanner) (core.SessionRecord, bool, error) {
	rec, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.SessionRecord{}, false, nil
	}
	if err != nil {
		return core.SessionRecord{}, false, err
	}
	return rec, true, nil
}

func ensureSessionTx(ctx context.Context, tx *sql.Tx, id, now string) error {
	metaJSON, err := marshalJSON(map[string]interface{}{})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(id, created_at, updated_at, meta_json) VALUES (?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, id, now, now, metaJSON)
	return err
}

func touchSessionTx(ctx context.Context, tx *sql.Tx, id, now string) error {
	_, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, now, id)
	return err
}

func loadInvokedSkills(ctx context.Context, db *sql.DB, id string) ([]core.InvokedSkillRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT skill_json FROM invoked_skills WHERE session_id = ? ORDER BY position ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.InvokedSkillRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var skill core.InvokedSkillRecord
		if err := json.Unmarshal([]byte(raw), &skill); err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, rows.Err()
}

func loadTasks(ctx context.Context, db *sql.DB, sessionID string) (map[string]core.Task, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	tasks, err := loadTasksTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func loadTasksTx(ctx context.Context, tx *sql.Tx, sessionID string) (map[string]core.Task, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, subject, description, active_form, status, owner, metadata_json, created_at, updated_at FROM tasks WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := map[string]core.Task{}
	for rows.Next() {
		var task core.Task
		var metadata sql.NullString
		task.SessionID = sessionID
		if err := rows.Scan(&task.ID, &task.Subject, &task.Description, &task.ActiveForm, &task.Status, &task.Owner, &metadata, &task.CreatedAt, &task.UpdatedAt); err != nil {
			return nil, err
		}
		task.Blocks = []string{}
		task.BlockedBy = []string{}
		if metadata.Valid && metadata.String != "" {
			if err := json.Unmarshal([]byte(metadata.String), &task.Metadata); err != nil {
				return nil, err
			}
		}
		tasks[task.ID] = task
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	edgeRows, err := tx.QueryContext(ctx, `SELECT blocker_id, blocked_id FROM task_edges WHERE session_id = ? ORDER BY blocker_id ASC, blocked_id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var blockerID, blockedID string
		if err := edgeRows.Scan(&blockerID, &blockedID); err != nil {
			return nil, err
		}
		blocker, blockerOK := tasks[blockerID]
		blocked, blockedOK := tasks[blockedID]
		if !blockerOK || !blockedOK {
			continue
		}
		blocker.Blocks = addID(blocker.Blocks, blockedID)
		blocked.BlockedBy = addID(blocked.BlockedBy, blockerID)
		tasks[blockerID] = blocker
		tasks[blockedID] = blocked
	}
	return tasks, edgeRows.Err()
}

func loadTaskTx(ctx context.Context, tx *sql.Tx, sessionID, taskID string) (core.Task, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, subject, description, active_form, status, owner, metadata_json, created_at, updated_at FROM tasks WHERE session_id = ? AND id = ?`, sessionID, taskID)
	var task core.Task
	var metadata sql.NullString
	task.SessionID = sessionID
	task.Blocks = []string{}
	task.BlockedBy = []string{}
	if err := row.Scan(&task.ID, &task.Subject, &task.Description, &task.ActiveForm, &task.Status, &task.Owner, &metadata, &task.CreatedAt, &task.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.Task{}, false, nil
		}
		return core.Task{}, false, err
	}
	if metadata.Valid && metadata.String != "" {
		if err := json.Unmarshal([]byte(metadata.String), &task.Metadata); err != nil {
			return core.Task{}, false, err
		}
	}
	return task, true, nil
}

func loadTaskEdgesTx(ctx context.Context, tx *sql.Tx, sessionID string, tasks map[string]*core.Task) error {
	if len(tasks) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT blocker_id, blocked_id FROM task_edges WHERE session_id = ? ORDER BY blocker_id ASC, blocked_id ASC`, sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var blockerID, blockedID string
		if err := rows.Scan(&blockerID, &blockedID); err != nil {
			return err
		}
		if blocker, ok := tasks[blockerID]; ok {
			blocker.Blocks = addID(blocker.Blocks, blockedID)
		}
		if blocked, ok := tasks[blockedID]; ok {
			blocked.BlockedBy = addID(blocked.BlockedBy, blockerID)
		}
	}
	return rows.Err()
}

func insertTaskTx(ctx context.Context, tx *sql.Tx, task core.Task) error {
	metadata, err := nullableJSON(task.Metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tasks(session_id, id, subject, description, active_form, status, owner, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.SessionID, task.ID, task.Subject, task.Description, task.ActiveForm, task.Status, task.Owner, metadata, task.CreatedAt, task.UpdatedAt,
	)
	return err
}

func updateTaskRowTx(ctx context.Context, tx *sql.Tx, task core.Task) error {
	metadata, err := nullableJSON(task.Metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE tasks
		SET subject = ?, description = ?, active_form = ?, status = ?, owner = ?, metadata_json = ?, updated_at = ?
		WHERE session_id = ? AND id = ?`,
		task.Subject, task.Description, task.ActiveForm, task.Status, task.Owner, metadata, task.UpdatedAt, task.SessionID, task.ID,
	)
	return err
}

func applyTaskEdgeChangesTx(ctx context.Context, tx *sql.Tx, sessionID, taskID string, task core.Task, patch core.TaskPatch) error {
	if task.Status == core.TaskDeleted {
		_, err := tx.ExecContext(ctx, `DELETE FROM task_edges WHERE session_id = ? AND (blocker_id = ? OR blocked_id = ?)`, sessionID, taskID, taskID)
		return err
	}
	for _, id := range uniqueIDs(patch.AddBlocks) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_edges(session_id, blocker_id, blocked_id) VALUES (?, ?, ?)`, sessionID, taskID, id); err != nil {
			return err
		}
	}
	for _, id := range uniqueIDs(patch.RemoveBlocks) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_edges WHERE session_id = ? AND blocker_id = ? AND blocked_id = ?`, sessionID, taskID, id); err != nil {
			return err
		}
	}
	for _, id := range uniqueIDs(patch.AddBlockedBy) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_edges(session_id, blocker_id, blocked_id) VALUES (?, ?, ?)`, sessionID, id, taskID); err != nil {
			return err
		}
	}
	for _, id := range uniqueIDs(patch.RemoveBlockedBy) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_edges WHERE session_id = ? AND blocker_id = ? AND blocked_id = ?`, sessionID, id, taskID); err != nil {
			return err
		}
	}
	return nil
}

func detachTask(tasks map[string]core.Task, taskID string, task *core.Task) {
	for _, id := range task.Blocks {
		if other, ok := tasks[id]; ok {
			other.BlockedBy = removeID(other.BlockedBy, taskID)
			tasks[id] = other
		}
	}
	for _, id := range task.BlockedBy {
		if other, ok := tasks[id]; ok {
			other.Blocks = removeID(other.Blocks, taskID)
			tasks[id] = other
		}
	}
	task.Blocks = []string{}
	task.BlockedBy = []string{}
}

func applyTaskEdges(tasks map[string]core.Task, taskID string, task *core.Task, patch core.TaskPatch) error {
	for _, id := range uniqueIDs(patch.AddBlocks) {
		if id == "" || id == taskID {
			return fmt.Errorf("invalid task dependency: %q", id)
		}
		other, ok := tasks[id]
		if !ok || other.Status == core.TaskDeleted {
			return fmt.Errorf("blocked task not found: %s", id)
		}
		task.Blocks = addID(task.Blocks, id)
		other.BlockedBy = addID(other.BlockedBy, taskID)
		tasks[id] = other
	}
	for _, id := range uniqueIDs(patch.RemoveBlocks) {
		task.Blocks = removeID(task.Blocks, id)
		if other, ok := tasks[id]; ok {
			other.BlockedBy = removeID(other.BlockedBy, taskID)
			tasks[id] = other
		}
	}
	for _, id := range uniqueIDs(patch.AddBlockedBy) {
		if id == "" || id == taskID {
			return fmt.Errorf("invalid task dependency: %q", id)
		}
		other, ok := tasks[id]
		if !ok || other.Status == core.TaskDeleted {
			return fmt.Errorf("blocking task not found: %s", id)
		}
		task.BlockedBy = addID(task.BlockedBy, id)
		other.Blocks = addID(other.Blocks, taskID)
		tasks[id] = other
	}
	for _, id := range uniqueIDs(patch.RemoveBlockedBy) {
		task.BlockedBy = removeID(task.BlockedBy, id)
		if other, ok := tasks[id]; ok {
			other.Blocks = removeID(other.Blocks, taskID)
			tasks[id] = other
		}
	}
	if hasTaskCycle(tasks, taskID, *task) {
		return fmt.Errorf("task dependency cycle detected")
	}
	return nil
}

func applyTaskEdgesTx(ctx context.Context, tx *sql.Tx, sessionID, taskID string, task *core.Task, patch core.TaskPatch) error {
	for _, id := range uniqueIDs(patch.AddBlocks) {
		if id == "" || id == taskID {
			return fmt.Errorf("invalid task dependency: %q", id)
		}
		if ok, err := activeTaskExistsTx(ctx, tx, sessionID, id); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("blocked task not found: %s", id)
		}
		task.Blocks = addID(task.Blocks, id)
	}
	for _, id := range uniqueIDs(patch.RemoveBlocks) {
		task.Blocks = removeID(task.Blocks, id)
	}
	for _, id := range uniqueIDs(patch.AddBlockedBy) {
		if id == "" || id == taskID {
			return fmt.Errorf("invalid task dependency: %q", id)
		}
		if ok, err := activeTaskExistsTx(ctx, tx, sessionID, id); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("blocking task not found: %s", id)
		}
		task.BlockedBy = addID(task.BlockedBy, id)
	}
	for _, id := range uniqueIDs(patch.RemoveBlockedBy) {
		task.BlockedBy = removeID(task.BlockedBy, id)
	}
	return nil
}

func activeTaskExistsTx(ctx context.Context, tx *sql.Tx, sessionID, taskID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE session_id = ? AND id = ? AND status != ?`, sessionID, taskID, core.TaskDeleted).Scan(&n)
	return n > 0, err
}

func taskPathExistsTx(ctx context.Context, tx *sql.Tx, sessionID, fromID, toID string) (bool, error) {
	if fromID == toID {
		return true, nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE reachable(id) AS (
			SELECT blocked_id FROM task_edges WHERE session_id = ? AND blocker_id = ?
			UNION
			SELECT task_edges.blocked_id
			FROM task_edges
			JOIN reachable ON task_edges.blocker_id = reachable.id
			JOIN tasks ON tasks.session_id = task_edges.session_id AND tasks.id = task_edges.blocked_id
			WHERE task_edges.session_id = ? AND tasks.status != ?
		)
		SELECT COUNT(*) FROM reachable WHERE id = ? LIMIT 1`, sessionID, fromID, sessionID, core.TaskDeleted, toID).Scan(&n)
	return n > 0, err
}

func uniqueIDs(ids []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func addID(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}

func hasTaskCycle(tasks map[string]core.Task, taskID string, updated core.Task) bool {
	graph := make(map[string][]string, len(tasks))
	for id, task := range tasks {
		if task.Status == core.TaskDeleted {
			continue
		}
		graph[id] = append([]string(nil), task.Blocks...)
	}
	graph[taskID] = append([]string(nil), updated.Blocks...)
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, next := range graph[id] {
			if visit(next) {
				return true
			}
		}
		visiting[id] = false
		visited[id] = true
		return false
	}
	for id := range graph {
		if visit(id) {
			return true
		}
	}
	return false
}

func nullableJSON(value interface{}) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := marshalJSON(value)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func marshalJSON(value interface{}) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
