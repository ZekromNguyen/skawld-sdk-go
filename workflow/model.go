// Package workflow provides the canonical, provider-independent workflow model
// and deterministic runtime.
package workflow

import (
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

const SchemaVersion = "1"

type VersionStatus string

const (
	VersionCandidate VersionStatus = "candidate"
	VersionPublished VersionStatus = "published"
	VersionRetired   VersionStatus = "retired"
)

type Workflow struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Version is immutable after publication. A new candidate must be created for
// every learned or human-authored change.
type Version struct {
	SchemaVersion string                 `json:"schema_version"`
	Workflow      Workflow               `json:"workflow"`
	Version       int                    `json:"version"`
	Status        VersionStatus          `json:"status"`
	InputSchema   map[string]interface{} `json:"input_schema,omitempty"`
	ContextSchema map[string]interface{} `json:"context_schema,omitempty"`
	// ToolCatalogDigest binds a learned workflow to the exact tool contracts
	// that were validated by its compiler.
	ToolCatalogDigest      string            `json:"tool_catalog_digest,omitempty"`
	Steps                  []Step            `json:"steps"`
	SourceDemonstrationIDs []string          `json:"source_demonstration_ids,omitempty"`
	Learning               *LearningMetadata `json:"learning,omitempty"`
	CreatedAt              time.Time         `json:"created_at"`
	CreatedBy              string            `json:"created_by,omitempty"`
	PublishedAt            time.Time         `json:"published_at,omitempty"`
	PublishedBy            string            `json:"published_by,omitempty"`
}

// LearningMetadata contains compiler-derived provenance for a learned
// candidate. It is informational and must never be used as authorization to
// publish or execute a workflow.
type LearningMetadata struct {
	DemonstrationCount      int     `json:"demonstration_count"`
	SequenceConsistency     float64 `json:"sequence_consistency"`
	CommonActionCount       int     `json:"common_action_count"`
	ParameterCandidateCount int     `json:"parameter_candidate_count"`
	StepEvidenceCoverage    float64 `json:"step_evidence_coverage"`
	RequiresHumanReview     bool    `json:"requires_human_review"`
}

type StepKind string

const (
	StepTool       StepKind = "tool"
	StepValidation StepKind = "validation"
	StepApproval   StepKind = "approval"
)

type Step struct {
	ID         string        `json:"id"`
	Name       string        `json:"name,omitempty"`
	Kind       StepKind      `json:"kind"`
	DependsOn  []string      `json:"depends_on,omitempty"`
	When       *Condition    `json:"when,omitempty"`
	Evidence   []EvidenceRef `json:"evidence,omitempty"`
	Tool       *ToolCall     `json:"tool,omitempty"`
	Validation *Validation   `json:"validation,omitempty"`
	Approval   *ApprovalSpec `json:"approval,omitempty"`
	Retry      RetryPolicy   `json:"retry,omitempty"`
	Timeout    time.Duration `json:"timeout,omitempty"`
}

// EvidenceRef links an extracted step back to immutable demonstration events.
// The learning compiler validates these references before saving a candidate.
type EvidenceRef struct {
	DemonstrationID string   `json:"demonstration_id"`
	EventIDs        []string `json:"event_ids"`
}

type ToolCall struct {
	Name           string           `json:"name"`
	Arguments      map[string]Value `json:"arguments,omitempty"`
	IdempotencyKey *Value           `json:"idempotency_key,omitempty"`
	Reason         string           `json:"reason,omitempty"`
}

type Validation struct {
	Condition Condition `json:"condition"`
	Message   string    `json:"message,omitempty"`
}

type ApprovalSpec struct {
	Summary string         `json:"summary"`
	Risk    core.RiskLevel `json:"risk"`
}

type RetryPolicy struct {
	MaxAttempts int           `json:"max_attempts,omitempty"`
	Backoff     time.Duration `json:"backoff,omitempty"`
}

// Value is either a literal or a reference such as input.invoice.amount,
// context.account_id, or steps.lookup.output.id. References are intentionally
// structural, avoiding an executable expression language.
type Value struct {
	Ref     string      `json:"ref,omitempty"`
	Literal interface{} `json:"literal,omitempty"`
}

type Operator string

const (
	OpEqual        Operator = "eq"
	OpNotEqual     Operator = "ne"
	OpGreater      Operator = "gt"
	OpGreaterEqual Operator = "gte"
	OpLess         Operator = "lt"
	OpLessEqual    Operator = "lte"
	OpExists       Operator = "exists"
	OpNotExists    Operator = "not_exists"
)

type Condition struct {
	Left     Value    `json:"left"`
	Operator Operator `json:"operator"`
	Right    Value    `json:"right,omitempty"`
}

type ExecutionStatus string

const (
	ExecutionRunning          ExecutionStatus = "running"
	ExecutionAwaitingApproval ExecutionStatus = "awaiting_approval"
	ExecutionRecoveryRequired ExecutionStatus = "recovery_required"
	ExecutionCompleted        ExecutionStatus = "completed"
	ExecutionFailed           ExecutionStatus = "failed"
	ExecutionCanceled         ExecutionStatus = "canceled"
)

type StepStatus string

const (
	StepPending          StepStatus = "pending"
	StepRunning          StepStatus = "running"
	StepSkipped          StepStatus = "skipped"
	StepAwaitingApproval StepStatus = "awaiting_approval"
	StepRecoveryRequired StepStatus = "recovery_required"
	StepCompleted        StepStatus = "completed"
	StepFailed           StepStatus = "failed"
	StepCanceled         StepStatus = "canceled"
)

type Execution struct {
	ID                string                 `json:"id"`
	WorkflowID        string                 `json:"workflow_id"`
	WorkflowVersion   int                    `json:"workflow_version"`
	Revision          int64                  `json:"revision,omitempty"`
	Principal         core.Principal         `json:"principal"`
	Status            ExecutionStatus        `json:"status"`
	Input             map[string]interface{} `json:"input"`
	Context           map[string]interface{} `json:"context,omitempty"`
	State             map[string]interface{} `json:"state"`
	Steps             []StepExecution        `json:"steps"`
	NextStep          int                    `json:"next_step"`
	PendingApprovalID string                 `json:"pending_approval_id,omitempty"`
	Approvals         map[string]string      `json:"approvals,omitempty"`
	StartedAt         time.Time              `json:"started_at"`
	DeadlineAt        time.Time              `json:"deadline_at,omitempty"`
	UpdatedAt         time.Time              `json:"updated_at,omitempty"`
	CompletedAt       time.Time              `json:"completed_at,omitempty"`
	Error             *ExecutionError        `json:"error,omitempty"`
}

type StepExecution struct {
	StepID      string                 `json:"step_id"`
	Status      StepStatus             `json:"status"`
	Attempts    int                    `json:"attempts"`
	Input       map[string]interface{} `json:"input,omitempty"`
	Output      interface{}            `json:"output,omitempty"`
	StartedAt   time.Time              `json:"started_at,omitempty"`
	CompletedAt time.Time              `json:"completed_at,omitempty"`
	Error       *ExecutionError        `json:"error,omitempty"`
}

type ExecutionError struct {
	Kind      core.ErrorKind `json:"kind"`
	StepID    string         `json:"step_id,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable,omitempty"`
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}
