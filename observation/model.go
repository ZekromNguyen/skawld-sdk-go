// Package observation models semantic human demonstrations. These events are
// distinct from core.Observation, which is operational telemetry.
package observation

import (
	"fmt"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

const SchemaVersion = "1"

type Source string

const (
	SourceBrowser  Source = "browser"
	SourceDesktop  Source = "desktop"
	SourceAPI      Source = "api"
	SourceCLI      Source = "cli"
	SourceDatabase Source = "database"
	SourceText     Source = "text"
	SourceFile     Source = "file"
	SourceEmail    Source = "email"
)

type Trust string

const (
	TrustSystemPolicy        Trust = "system_policy"
	TrustHumanInstruction    Trust = "human_instruction"
	TrustApplicationEvent    Trust = "application_event"
	TrustToolResult          Trust = "tool_result"
	TrustUntrustedContent    Trust = "untrusted_content"
	TrustModelInterpretation Trust = "model_interpretation"
)

// Sensitivity is an application-assigned data classification. Adapters must
// configure it from trusted deployment policy rather than accept a downgrade
// from an observed page, document, or request body.
type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityConfidential Sensitivity = "confidential"
	SensitivityRestricted   Sensitivity = "restricted"
)

type Entity struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type Event struct {
	SchemaVersion string                 `json:"schema_version"`
	ID            string                 `json:"id"`
	SessionID     string                 `json:"session_id"`
	Principal     core.Principal         `json:"principal"`
	Timestamp     time.Time              `json:"timestamp"`
	Source        Source                 `json:"source"`
	Trust         Trust                  `json:"trust"`
	Sensitivity   Sensitivity            `json:"sensitivity"`
	Application   string                 `json:"application,omitempty"`
	Action        string                 `json:"action"`
	Intent        string                 `json:"intent,omitempty"`
	Entity        *Entity                `json:"entity,omitempty"`
	Input         map[string]interface{} `json:"input,omitempty"`
	Output        map[string]interface{} `json:"output,omitempty"`
	Context       map[string]interface{} `json:"context,omitempty"`
	Decision      map[string]interface{} `json:"decision,omitempty"`
	Result        map[string]interface{} `json:"result,omitempty"`
	Error         string                 `json:"error,omitempty"`
	CorrectionOf  string                 `json:"correction_of,omitempty"`
	ApprovalID    string                 `json:"approval_id,omitempty"`
}

type DemonstrationStatus string

const (
	DemonstrationRecording DemonstrationStatus = "recording"
	DemonstrationCompleted DemonstrationStatus = "completed"
	DemonstrationRejected  DemonstrationStatus = "rejected"
)

type Demonstration struct {
	ID          string              `json:"id"`
	WorkflowKey string              `json:"workflow_key"`
	Principal   core.Principal      `json:"principal"`
	Status      DemonstrationStatus `json:"status"`
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt time.Time           `json:"completed_at,omitempty"`
	Trace       WorkflowTrace       `json:"trace"`
}

type WorkflowTrace struct {
	SchemaVersion  string                 `json:"schema_version"`
	SessionID      string                 `json:"session_id"`
	Events         []Event                `json:"events"`
	InitialContext map[string]interface{} `json:"initial_context,omitempty"`
	FinalResult    map[string]interface{} `json:"final_result,omitempty"`
}

func (t WorkflowTrace) Validate() error {
	if t.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported trace schema version %q", t.SchemaVersion)
	}
	if t.SessionID == "" {
		return fmt.Errorf("trace session_id is required")
	}
	seen := make(map[string]struct{}, len(t.Events))
	var previous time.Time
	for index, event := range t.Events {
		if event.ID == "" || event.Action == "" {
			return fmt.Errorf("trace event %d requires id and action", index)
		}
		if !validSource(event.Source) || !validTrust(event.Trust) ||
			!validSensitivity(event.Sensitivity) {
			return fmt.Errorf("trace event %q has invalid source or trust", event.ID)
		}
		if event.SchemaVersion != SchemaVersion || event.SessionID != t.SessionID {
			return fmt.Errorf("trace event %q has inconsistent schema or session", event.ID)
		}
		if event.Timestamp.IsZero() {
			return fmt.Errorf("trace event %q requires a timestamp", event.ID)
		}
		if event.Entity != nil && event.Entity.Type == "" {
			return fmt.Errorf("trace event %q entity requires a type", event.ID)
		}
		if _, ok := seen[event.ID]; ok {
			return fmt.Errorf("duplicate trace event id %q", event.ID)
		}
		if !previous.IsZero() && event.Timestamp.Before(previous) {
			return fmt.Errorf("trace event %q is out of order", event.ID)
		}
		if event.CorrectionOf != "" {
			if _, exists := seen[event.CorrectionOf]; !exists {
				return fmt.Errorf("trace event %q corrects unknown or later event %q", event.ID, event.CorrectionOf)
			}
		}
		seen[event.ID] = struct{}{}
		previous = event.Timestamp
	}
	return nil
}

// ValidateAppend verifies event identity, ordering, correction references, and
// schema fields against the current trace. Stores must call it atomically with
// append so concurrent or retried adapter deliveries cannot duplicate events.
func ValidateAppend(trace WorkflowTrace, event Event) error {
	if trace.SchemaVersion != SchemaVersion || trace.SessionID == "" {
		return appendValidationError("trace schema and session are required")
	}
	if event.SchemaVersion != SchemaVersion || event.SessionID != trace.SessionID {
		return appendValidationError("observation event has inconsistent schema or session")
	}
	if event.ID == "" || event.Action == "" || event.Timestamp.IsZero() {
		return appendValidationError("observation event requires id, action, and timestamp")
	}
	if !event.Principal.Valid() {
		return appendValidationError("observation event requires a principal")
	}
	if !validSource(event.Source) || !validTrust(event.Trust) ||
		!validSensitivity(event.Sensitivity) {
		return appendValidationError(
			"observation event has invalid source, trust, or sensitivity",
		)
	}
	if event.Entity != nil && event.Entity.Type == "" {
		return appendValidationError("observation event entity requires a type")
	}
	correctionExists := event.CorrectionOf == ""
	for _, existing := range trace.Events {
		if existing.ID == event.ID {
			return &core.SkawldError{
				Kind: core.ErrorConflict, Message: "observation event already exists",
			}
		}
		if existing.ID == event.CorrectionOf {
			correctionExists = true
		}
	}
	if !correctionExists {
		return appendValidationError(
			"observation correction references an unknown or later event",
		)
	}
	if count := len(trace.Events); count > 0 &&
		event.Timestamp.Before(trace.Events[count-1].Timestamp) {
		return appendValidationError(
			"observation timestamp is earlier than the previous event",
		)
	}
	return nil
}

func appendValidationError(message string) error {
	return &core.SkawldError{Kind: core.ErrorValidation, Message: message}
}

func validSource(source Source) bool {
	switch source {
	case SourceBrowser, SourceDesktop, SourceAPI, SourceCLI,
		SourceDatabase, SourceText, SourceFile, SourceEmail:
		return true
	default:
		return false
	}
}

func validTrust(trust Trust) bool {
	switch trust {
	case TrustSystemPolicy, TrustHumanInstruction, TrustApplicationEvent,
		TrustToolResult, TrustUntrustedContent, TrustModelInterpretation:
		return true
	default:
		return false
	}
}

func validSensitivity(sensitivity Sensitivity) bool {
	switch sensitivity {
	case "", SensitivityPublic, SensitivityInternal,
		SensitivityConfidential, SensitivityRestricted:
		return true
	default:
		return false
	}
}
