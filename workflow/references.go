package workflow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ValidateReferences verifies every structural workflow reference against
// trusted workflow input/context contracts and trusted tool output schemas.
// toolOutputs is keyed by tool name.
func ValidateReferences(
	version Version,
	toolOutputs map[string]map[string]interface{},
) error {
	prior := make(map[string]string, len(version.Steps))
	for _, step := range version.Steps {
		validateValue := func(value Value) error {
			if value.Ref == "" {
				return nil
			}
			return validateReference(
				value.Ref, version.InputSchema, version.ContextSchema, prior, toolOutputs,
			)
		}
		validateCondition := func(condition Condition) error {
			if err := validateValue(condition.Left); err != nil {
				return err
			}
			return validateValue(condition.Right)
		}
		if step.When != nil {
			if err := validateCondition(*step.When); err != nil {
				return fmt.Errorf("step %q when: %w", step.ID, err)
			}
		}
		if step.Tool != nil {
			for name, value := range step.Tool.Arguments {
				if err := validateValue(value); err != nil {
					return fmt.Errorf("step %q argument %q: %w", step.ID, name, err)
				}
			}
			if step.Tool.IdempotencyKey != nil {
				if err := validateValue(*step.Tool.IdempotencyKey); err != nil {
					return fmt.Errorf("step %q idempotency key: %w", step.ID, err)
				}
			}
		}
		if step.Validation != nil {
			if err := validateCondition(step.Validation.Condition); err != nil {
				return fmt.Errorf("step %q validation: %w", step.ID, err)
			}
		}
		toolName := ""
		if step.Tool != nil {
			toolName = step.Tool.Name
		}
		prior[step.ID] = toolName
	}
	return nil
}

// ValidateInputs validates supplied task data against the trusted structural
// contracts and ensures every external reference exists before any workflow
// step can create a side effect.
func ValidateInputs(
	version Version,
	input map[string]interface{},
	workflowContext map[string]interface{},
) error {
	if err := validateContractValue(version.InputSchema, input, "input"); err != nil {
		return err
	}
	if err := validateContractValue(version.ContextSchema, workflowContext, "context"); err != nil {
		return err
	}
	for _, step := range version.Steps {
		values := referencedValues(step)
		for _, value := range values {
			if value.Ref == "" {
				continue
			}
			parts := strings.Split(value.Ref, ".")
			if len(parts) < 2 || parts[0] == "steps" {
				continue
			}
			root := interface{}(input)
			if parts[0] == "context" {
				root = workflowContext
			}
			if !dataPathExists(root, parts[1:]) {
				return fmt.Errorf("required workflow reference %q is absent from execution data", value.Ref)
			}
		}
	}
	return nil
}

// ValidateOutput verifies an actual tool result against the trusted output
// schema used to compile and preflight workflow references.
func ValidateOutput(
	schema map[string]interface{},
	output interface{},
	toolName string,
) error {
	if len(schema) == 0 {
		return nil
	}
	path := "tool_output"
	if strings.TrimSpace(toolName) != "" {
		path = "tool_output." + toolName
	}
	if err := validateContractSchema(
		path+"_schema", schema, false, 0,
	); err != nil {
		return err
	}
	return validateContractValue(schema, output, path)
}

func referencedValues(step Step) []Value {
	values := make([]Value, 0)
	if step.When != nil {
		values = append(values, step.When.Left, step.When.Right)
	}
	if step.Tool != nil {
		for _, value := range step.Tool.Arguments {
			values = append(values, value)
		}
		if step.Tool.IdempotencyKey != nil {
			values = append(values, *step.Tool.IdempotencyKey)
		}
	}
	if step.Validation != nil {
		values = append(
			values, step.Validation.Condition.Left, step.Validation.Condition.Right,
		)
	}
	return values
}

func dataPathExists(value interface{}, path []string) bool {
	current := value
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]interface{}:
			var exists bool
			current, exists = typed[segment]
			if !exists {
				return false
			}
		case []interface{}:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	return true
}

func validateContractValue(
	schema map[string]interface{},
	value interface{},
	path string,
) error {
	if len(schema) == 0 {
		return nil
	}
	if rawAllowed, exists := schema["enum"]; exists {
		allowed, ok := schemaEnumValues(rawAllowed)
		if !ok {
			return fmt.Errorf("%s schema enum is invalid", path)
		}
		matched := false
		for _, candidate := range allowed {
			if reflect.DeepEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf(
				"%s must be one of the declared enum values", path,
			)
		}
	}
	kind, _ := schema["type"].(string)
	if kind != "" && !matchesSchemaType(kind, value) {
		return fmt.Errorf("%s must be %s", path, kind)
	}
	switch kind {
	case "object":
		object, _ := value.(map[string]interface{})
		properties, _ := schema["properties"].(map[string]interface{})
		for _, name := range schemaStringList(schema["required"]) {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		if additional, exists := schema["additionalProperties"].(bool); exists && !additional {
			for name := range object {
				if _, declared := properties[name]; !declared {
					return fmt.Errorf("%s.%s is not declared by the contract", path, name)
				}
			}
		}
		for name, raw := range properties {
			child, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			item, exists := object[name]
			if !exists {
				continue
			}
			if err := validateContractValue(child, item, path+"."+name); err != nil {
				return err
			}
		}
		if additional, exists := schema["additionalProperties"].(map[string]interface{}); exists {
			for name, item := range object {
				if _, declared := properties[name]; declared {
					continue
				}
				if err := validateContractValue(
					additional, item, path+"."+name,
				); err != nil {
					return err
				}
			}
		}
	case "array":
		items, _ := value.([]interface{})
		child, _ := schema["items"].(map[string]interface{})
		for index, item := range items {
			if err := validateContractValue(
				child, item, fmt.Sprintf("%s.%d", path, index),
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaStringList(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		output := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				output = append(output, text)
			}
		}
		return output
	default:
		return nil
	}
}

func matchesSchemaType(kind string, value interface{}) bool {
	switch kind {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		return numericKind(value)
	case "integer":
		return integerKind(value)
	default:
		return true
	}
}

func numericKind(value interface{}) bool {
	if value == nil {
		return false
	}
	if number, ok := value.(json.Number); ok {
		_, err := number.Float64()
		return err == nil
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func integerKind(value interface{}) bool {
	if value == nil {
		return false
	}
	if number, ok := value.(json.Number); ok {
		_, err := number.Int64()
		return err == nil
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		number := reflect.ValueOf(value).Convert(reflect.TypeOf(float64(0))).Float()
		return number == float64(int64(number))
	default:
		return false
	}
}

func validateReference(
	reference string,
	inputSchema map[string]interface{},
	contextSchema map[string]interface{},
	prior map[string]string,
	toolOutputs map[string]map[string]interface{},
) error {
	parts := strings.Split(reference, ".")
	if len(parts) < 2 {
		return fmt.Errorf("reference %q is incomplete", reference)
	}
	switch parts[0] {
	case "input":
		if !referenceSchemaPathExists(inputSchema, parts[1:]) {
			return fmt.Errorf("reference %q is not declared by the trusted input schema", reference)
		}
	case "context":
		if !referenceSchemaPathExists(contextSchema, parts[1:]) {
			return fmt.Errorf("reference %q is not declared by the trusted context schema", reference)
		}
	case "steps":
		if len(parts) < 3 || parts[2] != "output" {
			return fmt.Errorf("reference %q must use steps.<id>.output", reference)
		}
		toolName, exists := prior[parts[1]]
		if !exists {
			return fmt.Errorf("reference %q names an unknown or later step", reference)
		}
		if toolName == "" {
			return fmt.Errorf("reference %q does not name a tool step", reference)
		}
		outputSchema := toolOutputs[toolName]
		if len(outputSchema) == 0 {
			return fmt.Errorf("reference %q uses tool %q without an output schema", reference, toolName)
		}
		if !referenceSchemaPathExists(outputSchema, parts[3:]) {
			return fmt.Errorf("reference %q is not declared by tool %q output schema", reference, toolName)
		}
	default:
		return fmt.Errorf("reference %q has an unsupported root", reference)
	}
	return nil
}

func referenceSchemaPathExists(schema map[string]interface{}, path []string) bool {
	if len(schema) == 0 {
		return false
	}
	if len(path) == 0 {
		return true
	}
	current := schema
	for _, segment := range path {
		kind, _ := current["type"].(string)
		if kind == "array" {
			if _, err := strconv.Atoi(segment); err != nil {
				return false
			}
			items, ok := current["items"].(map[string]interface{})
			if !ok {
				return false
			}
			current = items
			continue
		}
		properties, _ := current["properties"].(map[string]interface{})
		raw, exists := properties[segment]
		if !exists {
			additional, ok := current["additionalProperties"].(map[string]interface{})
			if !ok {
				return false
			}
			current = additional
			continue
		}
		child, ok := raw.(map[string]interface{})
		if !ok {
			return false
		}
		current = child
	}
	return true
}
