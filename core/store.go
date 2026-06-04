package core

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
