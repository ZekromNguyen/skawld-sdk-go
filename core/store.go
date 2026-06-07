package core

import "context"

type InvokedSkillRecord struct {
	Name            string `json:"name"`
	SubstitutedBody string `json:"substitutedBody"`
	InvokedAt       int64  `json:"invokedAt"`
}

type SessionRecord struct {
	ID            string                 `json:"id"`
	CreatedAt     string                 `json:"created_at"`
	UpdatedAt     string                 `json:"updated_at"`
	Meta          map[string]interface{} `json:"meta"`
	InvokedSkills []InvokedSkillRecord   `json:"invokedSkills,omitempty"`
}

type StoredMessage struct {
	Seq        int     `json:"seq"`
	AppendedAt string  `json:"appended_at"`
	Message    Message `json:"message"`
}

type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskDeleted    TaskStatus = "deleted"
)

type Task struct {
	ID          string                 `json:"id"`
	SessionID   string                 `json:"session_id"`
	Subject     string                 `json:"subject"`
	Description string                 `json:"description"`
	ActiveForm  string                 `json:"active_form,omitempty"`
	Status      TaskStatus             `json:"status"`
	Owner       string                 `json:"owner,omitempty"`
	Blocks      []string               `json:"blocks"`
	BlockedBy   []string               `json:"blocked_by"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
}

type CreateTaskInput struct {
	Subject     string                 `json:"subject"`
	Description string                 `json:"description"`
	ActiveForm  string                 `json:"active_form,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type TaskPatch struct {
	Subject         *string                `json:"subject,omitempty"`
	Description     *string                `json:"description,omitempty"`
	ActiveForm      *string                `json:"active_form,omitempty"`
	Status          *TaskStatus            `json:"status,omitempty"`
	Owner           *string                `json:"owner,omitempty"`
	AddBlocks       []string               `json:"add_blocks,omitempty"`
	AddBlockedBy    []string               `json:"add_blocked_by,omitempty"`
	RemoveBlocks    []string               `json:"remove_blocks,omitempty"`
	RemoveBlockedBy []string               `json:"remove_blocked_by,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Delete          bool                   `json:"delete,omitempty"`
}

type SessionStore interface {
	Create(ctx context.Context, id string, meta map[string]interface{}) (SessionRecord, error)
	Load(ctx context.Context, id string) (SessionRecord, bool, error)
	LoadMessages(ctx context.Context, id string) ([]StoredMessage, error)
	AppendMessages(ctx context.Context, id string, messages []Message) ([]StoredMessage, error)
	UpdateMeta(ctx context.Context, id string, meta map[string]interface{}) (SessionRecord, error)
	SetInvokedSkills(ctx context.Context, id string, skills []InvokedSkillRecord) error
	List(ctx context.Context, limit, offset int) ([]SessionRecord, error)
	Delete(ctx context.Context, id string) error
	CreateTask(ctx context.Context, sessionID string, input CreateTaskInput) (Task, error)
	GetTask(ctx context.Context, sessionID, taskID string) (Task, bool, error)
	ListTasks(ctx context.Context, sessionID string) ([]Task, error)
	UpdateTask(ctx context.Context, sessionID, taskID string, patch TaskPatch) (Task, bool, error)
	DeleteTask(ctx context.Context, sessionID, taskID string) (bool, error)
	Close() error
}

// LegacySessionStore is the pre-context store contract. AdaptLegacySessionStore
// lets existing store implementations be used while migrating to SessionStore.
type LegacySessionStore interface {
	Create(id string, meta map[string]interface{}) (SessionRecord, error)
	Load(id string) (SessionRecord, bool, error)
	LoadMessages(id string) ([]StoredMessage, error)
	AppendMessages(id string, messages []Message) ([]StoredMessage, error)
	UpdateMeta(id string, meta map[string]interface{}) (SessionRecord, error)
	SetInvokedSkills(id string, skills []InvokedSkillRecord) error
	List(limit, offset int) ([]SessionRecord, error)
	Delete(id string) error
	CreateTask(sessionID string, input CreateTaskInput) (Task, error)
	GetTask(sessionID, taskID string) (Task, bool, error)
	ListTasks(sessionID string) ([]Task, error)
	UpdateTask(sessionID, taskID string, patch TaskPatch) (Task, bool, error)
	DeleteTask(sessionID, taskID string) (bool, error)
	Close() error
}

type legacySessionStoreAdapter struct {
	store LegacySessionStore
}

func AdaptLegacySessionStore(store LegacySessionStore) SessionStore {
	return legacySessionStoreAdapter{store: store}
}

func (a legacySessionStoreAdapter) Create(ctx context.Context, id string, meta map[string]interface{}) (SessionRecord, error) {
	if err := contextError(ctx); err != nil {
		return SessionRecord{}, err
	}
	return a.store.Create(id, meta)
}

func (a legacySessionStoreAdapter) Load(ctx context.Context, id string) (SessionRecord, bool, error) {
	if err := contextError(ctx); err != nil {
		return SessionRecord{}, false, err
	}
	return a.store.Load(id)
}

func (a legacySessionStoreAdapter) LoadMessages(ctx context.Context, id string) ([]StoredMessage, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return a.store.LoadMessages(id)
}

func (a legacySessionStoreAdapter) AppendMessages(ctx context.Context, id string, messages []Message) ([]StoredMessage, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return a.store.AppendMessages(id, messages)
}

func (a legacySessionStoreAdapter) UpdateMeta(ctx context.Context, id string, meta map[string]interface{}) (SessionRecord, error) {
	if err := contextError(ctx); err != nil {
		return SessionRecord{}, err
	}
	return a.store.UpdateMeta(id, meta)
}

func (a legacySessionStoreAdapter) SetInvokedSkills(ctx context.Context, id string, skills []InvokedSkillRecord) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	return a.store.SetInvokedSkills(id, skills)
}

func (a legacySessionStoreAdapter) List(ctx context.Context, limit, offset int) ([]SessionRecord, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return a.store.List(limit, offset)
}

func (a legacySessionStoreAdapter) Delete(ctx context.Context, id string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	return a.store.Delete(id)
}

func (a legacySessionStoreAdapter) CreateTask(ctx context.Context, sessionID string, input CreateTaskInput) (Task, error) {
	if err := contextError(ctx); err != nil {
		return Task{}, err
	}
	return a.store.CreateTask(sessionID, input)
}

func (a legacySessionStoreAdapter) GetTask(ctx context.Context, sessionID, taskID string) (Task, bool, error) {
	if err := contextError(ctx); err != nil {
		return Task{}, false, err
	}
	return a.store.GetTask(sessionID, taskID)
}

func (a legacySessionStoreAdapter) ListTasks(ctx context.Context, sessionID string) ([]Task, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return a.store.ListTasks(sessionID)
}

func (a legacySessionStoreAdapter) UpdateTask(ctx context.Context, sessionID, taskID string, patch TaskPatch) (Task, bool, error) {
	if err := contextError(ctx); err != nil {
		return Task{}, false, err
	}
	return a.store.UpdateTask(sessionID, taskID, patch)
}

func (a legacySessionStoreAdapter) DeleteTask(ctx context.Context, sessionID, taskID string) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	return a.store.DeleteTask(sessionID, taskID)
}

func (a legacySessionStoreAdapter) Close() error {
	return a.store.Close()
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
