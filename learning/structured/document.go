package structured

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

const (
	maxCandidateSteps    = 128
	maxEvidencePerStep   = 64
	maxCandidateTimeout  = 10 * time.Minute
	maxCandidateBackoff  = time.Minute
	maxCandidateAttempts = 3
)

type candidateDocument struct {
	Description string          `json:"description,omitempty"`
	Steps       []candidateStep `json:"steps"`
}

type candidateStep struct {
	ID         string               `json:"id"`
	Name       string               `json:"name,omitempty"`
	Kind       workflow.StepKind    `json:"kind"`
	DependsOn  []string             `json:"depends_on,omitempty"`
	When       *candidateCondition  `json:"when,omitempty"`
	Evidence   []candidateEvidence  `json:"evidence,omitempty"`
	Tool       *candidateToolCall   `json:"tool,omitempty"`
	Validation *candidateValidation `json:"validation,omitempty"`
	Approval   *candidateApproval   `json:"approval,omitempty"`
	Retry      candidateRetry       `json:"retry,omitempty"`
	Timeout    string               `json:"timeout,omitempty"`
}

type candidateEvidence struct {
	DemonstrationID string   `json:"demonstration_id"`
	EventIDs        []string `json:"event_ids"`
}

type candidateToolCall struct {
	Name           string                    `json:"name"`
	Arguments      map[string]candidateValue `json:"arguments,omitempty"`
	IdempotencyKey *candidateValue           `json:"idempotency_key,omitempty"`
}

type candidateValidation struct {
	Condition candidateCondition `json:"condition"`
	Message   string             `json:"message,omitempty"`
}

type candidateApproval struct {
	Summary string         `json:"summary"`
	Risk    core.RiskLevel `json:"risk"`
}

type candidateRetry struct {
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Backoff     string `json:"backoff,omitempty"`
}

type candidateValue struct {
	Ref     string          `json:"ref,omitempty"`
	Literal json.RawMessage `json:"literal,omitempty"`
}

type candidateCondition struct {
	Left     candidateValue    `json:"left"`
	Operator workflow.Operator `json:"operator"`
	Right    *candidateValue   `json:"right,omitempty"`
}

func (e *Extractor) convertCandidate(
	request learning.ExtractionRequest,
	document candidateDocument,
	evidence evidenceIndex,
) (workflow.Version, error) {
	if len(document.Steps) == 0 || len(document.Steps) > maxCandidateSteps {
		return workflow.Version{}, validationError("workflow candidate must contain between 1 and 128 steps", nil)
	}
	if len(document.Description) > 4096 || containsUnsafeControl(document.Description) {
		return workflow.Version{}, validationError("workflow candidate description is invalid", nil)
	}
	candidate := workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow: workflow.Workflow{
			ID: request.WorkflowID, TenantID: request.TenantID,
			Name: request.WorkflowName, Description: strings.TrimSpace(document.Description),
		},
		Version: request.NextVersion, Status: workflow.VersionCandidate,
		InputSchema: request.InputSchema, ContextSchema: request.ContextSchema,
		CreatedAt: e.now(),
	}
	for _, demonstration := range request.Demonstrations {
		candidate.SourceDemonstrationIDs = append(candidate.SourceDemonstrationIDs, demonstration.ID)
	}

	prior := make(map[string]string, len(document.Steps))
	for index, proposed := range document.Steps {
		step, err := e.convertStep(
			proposed, evidence, prior, request.InputSchema, request.ContextSchema,
		)
		if err != nil {
			return workflow.Version{}, validationError(
				fmt.Sprintf("validate candidate step %d (%q)", index, proposed.ID), err,
			)
		}
		candidate.Steps = append(candidate.Steps, step)
		toolName := ""
		if step.Tool != nil {
			toolName = step.Tool.Name
		}
		prior[step.ID] = toolName
	}
	if err := candidate.Validate(); err != nil {
		return workflow.Version{}, validationError("validate workflow candidate", err)
	}
	return candidate, nil
}

func (e *Extractor) convertStep(
	proposed candidateStep,
	evidence evidenceIndex,
	prior map[string]string,
	inputSchema map[string]interface{},
	contextSchema map[string]interface{},
) (workflow.Step, error) {
	if !validIdentifier(proposed.ID, 128) {
		return workflow.Step{}, fmt.Errorf("step id is invalid")
	}
	if len(proposed.Name) > 256 || containsUnsafeControl(proposed.Name) {
		return workflow.Step{}, fmt.Errorf("step name is invalid")
	}
	step := workflow.Step{
		ID: proposed.ID, Name: strings.TrimSpace(proposed.Name), Kind: proposed.Kind,
		DependsOn: append([]string(nil), proposed.DependsOn...),
	}
	for _, dependency := range step.DependsOn {
		if _, exists := prior[dependency]; !exists {
			return workflow.Step{}, fmt.Errorf("dependency %q is unknown or not earlier", dependency)
		}
	}
	if proposed.When != nil {
		condition, err := e.convertCondition(
			*proposed.When, prior, inputSchema, contextSchema,
		)
		if err != nil {
			return workflow.Step{}, fmt.Errorf("when: %w", err)
		}
		step.When = &condition
	}
	if len(proposed.Evidence) > maxEvidencePerStep {
		return workflow.Step{}, fmt.Errorf("too many evidence references")
	}
	hasTrustedEvidence := false
	for _, item := range proposed.Evidence {
		events, exists := evidence[item.DemonstrationID]
		if !exists || len(item.EventIDs) == 0 || len(item.EventIDs) > maxEvidencePerStep {
			return workflow.Step{}, fmt.Errorf("evidence references an unknown demonstration or has invalid event count")
		}
		converted := workflow.EvidenceRef{DemonstrationID: item.DemonstrationID}
		for _, eventID := range item.EventIDs {
			event, exists := events[eventID]
			if !exists {
				return workflow.Step{}, fmt.Errorf("evidence references unknown event %q", eventID)
			}
			if trustedEvidence(event.trust) {
				hasTrustedEvidence = true
			}
			converted.EventIDs = append(converted.EventIDs, eventID)
		}
		step.Evidence = append(step.Evidence, converted)
	}

	timeout, err := parseBoundedDuration(proposed.Timeout, maxCandidateTimeout, "timeout")
	if err != nil {
		return workflow.Step{}, err
	}
	step.Timeout = timeout
	if proposed.Retry.MaxAttempts < 0 || proposed.Retry.MaxAttempts > maxCandidateAttempts {
		return workflow.Step{}, fmt.Errorf("retry max_attempts must be between 0 and 3")
	}
	backoff, err := parseBoundedDuration(proposed.Retry.Backoff, maxCandidateBackoff, "retry backoff")
	if err != nil {
		return workflow.Step{}, err
	}
	step.Retry = workflow.RetryPolicy{MaxAttempts: proposed.Retry.MaxAttempts, Backoff: backoff}

	switch proposed.Kind {
	case workflow.StepTool:
		if proposed.Tool == nil || proposed.Validation != nil || proposed.Approval != nil {
			return workflow.Step{}, fmt.Errorf("tool step must contain only a tool definition")
		}
		if !hasTrustedEvidence && !e.allowUntrustedEvidence {
			return workflow.Step{}, fmt.Errorf("tool step requires evidence from a trusted observation")
		}
		if _, allowed := e.toolNames[proposed.Tool.Name]; !allowed {
			return workflow.Step{}, fmt.Errorf("tool %q is not in the trusted extraction catalog", proposed.Tool.Name)
		}
		if err := e.validateToolArguments(proposed.Tool.Name, proposed.Tool.Arguments); err != nil {
			return workflow.Step{}, err
		}
		call := workflow.ToolCall{Name: proposed.Tool.Name}
		if len(proposed.Tool.Arguments) > 128 {
			return workflow.Step{}, fmt.Errorf("tool has too many arguments")
		}
		if len(proposed.Tool.Arguments) > 0 {
			call.Arguments = make(map[string]workflow.Value, len(proposed.Tool.Arguments))
		}
		for name, value := range proposed.Tool.Arguments {
			if !validIdentifier(name, 128) {
				return workflow.Step{}, fmt.Errorf("tool argument name %q is invalid", name)
			}
			converted, err := e.convertValue(value, prior, inputSchema, contextSchema)
			if err != nil {
				return workflow.Step{}, fmt.Errorf("tool argument %q: %w", name, err)
			}
			call.Arguments[name] = converted
		}
		if proposed.Tool.IdempotencyKey != nil {
			converted, err := e.convertValue(
				*proposed.Tool.IdempotencyKey, prior, inputSchema, contextSchema,
			)
			if err != nil {
				return workflow.Step{}, fmt.Errorf("idempotency key: %w", err)
			}
			call.IdempotencyKey = &converted
		}
		step.Tool = &call
	case workflow.StepValidation:
		if proposed.Validation == nil || proposed.Tool != nil || proposed.Approval != nil ||
			proposed.Retry.MaxAttempts != 0 || proposed.Retry.Backoff != "" {
			return workflow.Step{}, fmt.Errorf("validation step contains fields for another step kind")
		}
		if !hasTrustedEvidence && !e.allowUntrustedEvidence {
			return workflow.Step{}, fmt.Errorf("validation step requires evidence from a trusted observation")
		}
		condition, err := e.convertCondition(
			proposed.Validation.Condition, prior, inputSchema, contextSchema,
		)
		if err != nil {
			return workflow.Step{}, err
		}
		if len(proposed.Validation.Message) > 1024 || containsUnsafeControl(proposed.Validation.Message) {
			return workflow.Step{}, fmt.Errorf("validation message is invalid")
		}
		step.Validation = &workflow.Validation{
			Condition: condition, Message: strings.TrimSpace(proposed.Validation.Message),
		}
	case workflow.StepApproval:
		if proposed.Approval == nil || proposed.Tool != nil || proposed.Validation != nil ||
			proposed.Retry.MaxAttempts != 0 || proposed.Retry.Backoff != "" {
			return workflow.Step{}, fmt.Errorf("approval step contains fields for another step kind")
		}
		if strings.TrimSpace(proposed.Approval.Summary) == "" ||
			len(proposed.Approval.Summary) > 1024 || containsUnsafeControl(proposed.Approval.Summary) {
			return workflow.Step{}, fmt.Errorf("approval summary is invalid")
		}
		switch proposed.Approval.Risk {
		case core.RiskLow, core.RiskMedium, core.RiskHigh, core.RiskCritical:
		default:
			return workflow.Step{}, fmt.Errorf("approval risk is invalid")
		}
		step.Approval = &workflow.ApprovalSpec{
			Summary: strings.TrimSpace(proposed.Approval.Summary), Risk: proposed.Approval.Risk,
		}
	default:
		return workflow.Step{}, fmt.Errorf("unsupported step kind %q", proposed.Kind)
	}
	return step, nil
}

func (e *Extractor) validateToolArguments(
	toolName string,
	arguments map[string]candidateValue,
) error {
	definition := e.toolDefinitions[toolName]
	properties, hasProperties := definition.InputSchema["properties"].(map[string]interface{})
	if hasProperties {
		for name := range arguments {
			if _, exists := properties[name]; !exists {
				return fmt.Errorf("tool argument %q is not declared by %q", name, toolName)
			}
		}
	}
	required, _ := definition.InputSchema["required"].([]interface{})
	for _, raw := range required {
		name, ok := raw.(string)
		if !ok {
			continue
		}
		if _, exists := arguments[name]; !exists {
			return fmt.Errorf("tool argument %q is required by %q", name, toolName)
		}
	}
	return nil
}

func (e *Extractor) convertCondition(
	proposed candidateCondition,
	prior map[string]string,
	inputSchema map[string]interface{},
	contextSchema map[string]interface{},
) (workflow.Condition, error) {
	left, err := e.convertValue(proposed.Left, prior, inputSchema, contextSchema)
	if err != nil {
		return workflow.Condition{}, fmt.Errorf("left: %w", err)
	}
	condition := workflow.Condition{Left: left, Operator: proposed.Operator}
	switch proposed.Operator {
	case workflow.OpExists, workflow.OpNotExists:
		if proposed.Right != nil {
			return workflow.Condition{}, fmt.Errorf("%s condition must not contain right", proposed.Operator)
		}
	case workflow.OpEqual, workflow.OpNotEqual, workflow.OpGreater, workflow.OpGreaterEqual,
		workflow.OpLess, workflow.OpLessEqual:
		if proposed.Right == nil {
			return workflow.Condition{}, fmt.Errorf("%s condition requires right", proposed.Operator)
		}
		right, err := e.convertValue(*proposed.Right, prior, inputSchema, contextSchema)
		if err != nil {
			return workflow.Condition{}, fmt.Errorf("right: %w", err)
		}
		condition.Right = right
	default:
		return workflow.Condition{}, fmt.Errorf("unsupported condition operator %q", proposed.Operator)
	}
	return condition, nil
}

func (e *Extractor) convertValue(
	proposed candidateValue,
	prior map[string]string,
	inputSchema map[string]interface{},
	contextSchema map[string]interface{},
) (workflow.Value, error) {
	hasRef := strings.TrimSpace(proposed.Ref) != ""
	hasLiteral := len(proposed.Literal) != 0
	if hasRef == hasLiteral {
		return workflow.Value{}, fmt.Errorf("value must contain exactly one of ref or literal")
	}
	if hasRef {
		ref := strings.TrimSpace(proposed.Ref)
		if err := e.validateReference(ref, prior, inputSchema, contextSchema); err != nil {
			return workflow.Value{}, err
		}
		return workflow.Value{Ref: ref}, nil
	}
	if !e.allowLiterals {
		return workflow.Value{}, fmt.Errorf("literal values are disabled; use an input or context reference")
	}
	decoder := json.NewDecoder(bytes.NewReader(proposed.Literal))
	decoder.UseNumber()
	var literal interface{}
	if err := decoder.Decode(&literal); err != nil {
		return workflow.Value{}, fmt.Errorf("decode literal: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err == nil {
		return workflow.Value{}, fmt.Errorf("literal contains multiple JSON values")
	}
	return workflow.Value{Literal: literal}, nil
}

func (e *Extractor) validateReference(
	reference string,
	prior map[string]string,
	inputSchema map[string]interface{},
	contextSchema map[string]interface{},
) error {
	if len(reference) > 512 || strings.ContainsAny(reference, "\r\n\x00") {
		return fmt.Errorf("reference is invalid")
	}
	parts := strings.Split(reference, ".")
	if len(parts) < 2 {
		return fmt.Errorf("reference %q must use input.*, context.*, or steps.*.output", reference)
	}
	for _, part := range parts {
		if !validIdentifier(part, 128) {
			return fmt.Errorf("reference %q contains an invalid segment", reference)
		}
	}
	switch parts[0] {
	case "input":
		if !schemaPathExists(inputSchema, parts[1:]) {
			return fmt.Errorf("reference %q is not declared by the trusted input schema", reference)
		}
		return nil
	case "context":
		if !schemaPathExists(contextSchema, parts[1:]) {
			return fmt.Errorf("reference %q is not declared by the trusted context schema", reference)
		}
		return nil
	case "steps":
		if len(parts) < 3 || parts[2] != "output" {
			return fmt.Errorf("step reference %q must use steps.<id>.output", reference)
		}
		toolName, exists := prior[parts[1]]
		if !exists {
			return fmt.Errorf("step reference %q names an unknown or later step", reference)
		}
		if toolName == "" {
			return fmt.Errorf("step reference %q does not refer to a tool output", reference)
		}
		outputSchema := e.toolDefinitions[toolName].OutputSchema
		if len(outputSchema) == 0 {
			return fmt.Errorf("step reference %q uses tool %q without a declared output schema", reference, toolName)
		}
		if !schemaPathExists(outputSchema, parts[3:]) {
			return fmt.Errorf("step reference %q is not declared by tool %q output schema", reference, toolName)
		}
		return nil
	default:
		return fmt.Errorf("reference %q uses an unsupported root", reference)
	}
}

func parseBoundedDuration(value string, max time.Duration, field string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	if len(value) > 32 || strings.ContainsAny(value, "\r\n\x00") {
		return 0, fmt.Errorf("%s is invalid", field)
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 || duration > max {
		return 0, fmt.Errorf("%s must be a duration between zero and %s", field, max)
	}
	return duration, nil
}

func trustedEvidence(trust observation.Trust) bool {
	switch trust {
	case observation.TrustSystemPolicy, observation.TrustHumanInstruction,
		observation.TrustApplicationEvent, observation.TrustToolResult:
		return true
	default:
		return false
	}
}

func containsUnsafeControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return true
		}
	}
	return false
}
