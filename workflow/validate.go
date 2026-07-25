package workflow

import (
	"fmt"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func (version Version) Validate() error {
	if version.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported workflow schema version %q", version.SchemaVersion)
	}
	if strings.TrimSpace(version.Workflow.ID) == "" || strings.TrimSpace(version.Workflow.Name) == "" {
		return fmt.Errorf("workflow id and name are required")
	}
	if version.Version < 1 {
		return fmt.Errorf("workflow version must be at least 1")
	}
	switch version.Status {
	case VersionCandidate, VersionPublished, VersionRetired:
	default:
		return fmt.Errorf("invalid workflow version status %q", version.Status)
	}
	if len(version.Steps) == 0 {
		return fmt.Errorf("workflow must contain at least one step")
	}
	if err := validateContractSchema("input_schema", version.InputSchema, true, 0); err != nil {
		return err
	}
	if err := validateContractSchema("context_schema", version.ContextSchema, true, 0); err != nil {
		return err
	}
	if version.Learning != nil {
		if version.Learning.DemonstrationCount < 1 {
			return fmt.Errorf("learning metadata requires at least one demonstration")
		}
		if version.Learning.SequenceConsistency < 0 || version.Learning.SequenceConsistency > 1 {
			return fmt.Errorf("learning sequence consistency must be between 0 and 1")
		}
		if version.Learning.StepEvidenceCoverage < 0 || version.Learning.StepEvidenceCoverage > 1 {
			return fmt.Errorf("learning step evidence coverage must be between 0 and 1")
		}
		if version.Learning.CommonActionCount < 0 || version.Learning.ParameterCandidateCount < 0 {
			return fmt.Errorf("learning metadata counts must not be negative")
		}
	}
	seen := make(map[string]struct{}, len(version.Steps))
	for index, step := range version.Steps {
		if strings.TrimSpace(step.ID) == "" {
			return fmt.Errorf("workflow step %d requires an id", index)
		}
		if _, exists := seen[step.ID]; exists {
			return fmt.Errorf("duplicate workflow step id %q", step.ID)
		}
		for _, dependency := range step.DependsOn {
			if _, exists := seen[dependency]; !exists {
				return fmt.Errorf("step %q depends on unknown or later step %q", step.ID, dependency)
			}
		}
		if err := validateStep(step); err != nil {
			return fmt.Errorf("step %q: %w", step.ID, err)
		}
		seen[step.ID] = struct{}{}
	}
	return nil
}

func validateContractSchema(
	path string,
	schema map[string]interface{},
	root bool,
	depth int,
) error {
	if len(schema) == 0 {
		return nil
	}
	if depth > 32 {
		return fmt.Errorf("%s exceeds maximum nesting", path)
	}
	if rawType, exists := schema["type"]; exists {
		kind, ok := rawType.(string)
		if !ok {
			return fmt.Errorf("%s type must be a string", path)
		}
		switch kind {
		case "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return fmt.Errorf("%s has unsupported type %q", path, kind)
		}
		if root && kind != "object" {
			return fmt.Errorf("%s root type must be object", path)
		}
	}
	if rawProperties, exists := schema["properties"]; exists {
		properties, ok := rawProperties.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s properties must be an object", path)
		}
		for name, raw := range properties {
			child, ok := raw.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s property %q must be a schema object", path, name)
			}
			if err := validateContractSchema(path+"."+name, child, false, depth+1); err != nil {
				return err
			}
		}
	}
	if rawItems, exists := schema["items"]; exists {
		items, ok := rawItems.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s items must be a schema object", path)
		}
		if err := validateContractSchema(path+"[]", items, false, depth+1); err != nil {
			return err
		}
	}
	if rawRequired, exists := schema["required"]; exists {
		required, ok := schemaStringValues(rawRequired)
		if !ok {
			return fmt.Errorf("%s required must be an array", path)
		}
		for _, name := range required {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf(
					"%s required contains an empty property name", path,
				)
			}
		}
	}
	if rawEnum, exists := schema["enum"]; exists {
		values, ok := schemaEnumValues(rawEnum)
		if !ok || len(values) == 0 {
			return fmt.Errorf("%s enum must be a non-empty array", path)
		}
	}
	if rawAdditional, exists := schema["additionalProperties"]; exists {
		switch additional := rawAdditional.(type) {
		case bool:
		case map[string]interface{}:
			if err := validateContractSchema(
				path+".*", additional, false, depth+1,
			); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s additionalProperties must be boolean or a schema object", path)
		}
	}
	return nil
}

func schemaStringValues(value interface{}) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []interface{}:
		output := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			output = append(output, text)
		}
		return output, true
	default:
		return nil, false
	}
}

func schemaEnumValues(value interface{}) ([]interface{}, bool) {
	switch typed := value.(type) {
	case []interface{}:
		return typed, true
	case []string:
		output := make([]interface{}, len(typed))
		for index, item := range typed {
			output[index] = item
		}
		return output, true
	default:
		return nil, false
	}
}

func validateStep(step Step) error {
	if step.Timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	if step.Retry.MaxAttempts < 0 || step.Retry.Backoff < 0 {
		return fmt.Errorf("retry values must not be negative")
	}
	if step.When != nil {
		if err := validateCondition(*step.When); err != nil {
			return fmt.Errorf("when: %w", err)
		}
	}
	seenEvidence := make(map[string]struct{}, len(step.Evidence))
	for index, evidence := range step.Evidence {
		if strings.TrimSpace(evidence.DemonstrationID) == "" || len(evidence.EventIDs) == 0 {
			return fmt.Errorf("evidence %d requires a demonstration and at least one event", index)
		}
		for _, eventID := range evidence.EventIDs {
			if strings.TrimSpace(eventID) == "" {
				return fmt.Errorf("evidence %d contains an empty event id", index)
			}
			key := evidence.DemonstrationID + "\x00" + eventID
			if _, exists := seenEvidence[key]; exists {
				return fmt.Errorf("duplicate evidence reference for demonstration %q event %q", evidence.DemonstrationID, eventID)
			}
			seenEvidence[key] = struct{}{}
		}
	}
	switch step.Kind {
	case StepTool:
		if step.Tool == nil || strings.TrimSpace(step.Tool.Name) == "" {
			return fmt.Errorf("tool step requires a tool name")
		}
		if step.Validation != nil || step.Approval != nil {
			return fmt.Errorf("tool step contains fields for another step kind")
		}
	case StepValidation:
		if step.Validation == nil {
			return fmt.Errorf("validation step requires a condition")
		}
		if err := validateCondition(step.Validation.Condition); err != nil {
			return err
		}
		if step.Tool != nil || step.Approval != nil || step.Retry.MaxAttempts > 0 {
			return fmt.Errorf("validation step contains tool, approval, or retry fields")
		}
	case StepApproval:
		if step.Approval == nil || strings.TrimSpace(step.Approval.Summary) == "" {
			return fmt.Errorf("approval step requires a summary")
		}
		if step.Approval.Risk == "" {
			step.Approval.Risk = core.RiskHigh
		}
		if step.Tool != nil || step.Validation != nil || step.Retry.MaxAttempts > 0 {
			return fmt.Errorf("approval step contains tool, validation, or retry fields")
		}
	default:
		return fmt.Errorf("unsupported step kind %q", step.Kind)
	}
	return nil
}

func validateCondition(condition Condition) error {
	switch condition.Operator {
	case OpEqual, OpNotEqual, OpGreater, OpGreaterEqual, OpLess, OpLessEqual:
		if condition.Left.Ref == "" && condition.Left.Literal == nil {
			return fmt.Errorf("left value is required")
		}
	case OpExists, OpNotExists:
		if condition.Left.Ref == "" {
			return fmt.Errorf("%s requires a left reference", condition.Operator)
		}
	default:
		return fmt.Errorf("unsupported condition operator %q", condition.Operator)
	}
	return nil
}
