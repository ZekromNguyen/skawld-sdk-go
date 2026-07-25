package structured

import (
	"fmt"
	"strings"
)

var schemaAnnotationKeys = map[string]struct{}{
	"$comment": {}, "description": {}, "title": {}, "examples": {},
	"example": {}, "default": {}, "deprecated": {}, "readOnly": {},
	"writeOnly": {},
}

// sanitizeSchema copies structural JSON Schema data while removing prose and
// annotations that could carry indirect prompt-injection instructions.
func sanitizeSchema(schema map[string]interface{}) map[string]interface{} {
	value, ok := sanitizeSchemaValue(schema, 0).(map[string]interface{})
	if !ok {
		return nil
	}
	return value
}

func sanitizeSchemaValue(value interface{}, depth int) interface{} {
	if depth > 24 {
		return nil
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			if _, annotation := schemaAnnotationKeys[key]; annotation {
				continue
			}
			if len(key) == 0 || len(key) > 128 || strings.ContainsAny(key, "\r\n\x00") {
				continue
			}
			if sanitized := sanitizeSchemaValue(item, depth+1); sanitized != nil {
				out[key] = sanitized
			}
		}
		return out
	case []interface{}:
		limit := len(typed)
		if limit > 128 {
			limit = 128
		}
		out := make([]interface{}, 0, limit)
		for _, item := range typed[:limit] {
			if sanitized := sanitizeSchemaValue(item, depth+1); sanitized != nil {
				out = append(out, sanitized)
			}
		}
		return out
	case []string:
		limit := len(typed)
		if limit > 128 {
			limit = 128
		}
		out := make([]interface{}, 0, limit)
		for _, item := range typed[:limit] {
			if sanitized := sanitizeSchemaValue(item, depth+1); sanitized != nil {
				out = append(out, sanitized)
			}
		}
		return out
	case string:
		if len(typed) > 256 || strings.ContainsAny(typed, "\r\n\x00") {
			return nil
		}
		return typed
	case bool, float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return typed
	default:
		return nil
	}
}

// normalizeTrustedSchema accepts a deliberately small structural subset.
// Contracts cannot carry constants, regexes, defaults, or prompt prose.
func normalizeTrustedSchema(
	schema map[string]interface{},
	requireObjectRoot bool,
) (map[string]interface{}, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	return normalizeSchemaNode(schema, 0, requireObjectRoot)
}

func normalizeSchemaNode(
	schema map[string]interface{},
	depth int,
	requireObject bool,
) (map[string]interface{}, error) {
	if depth > 16 {
		return nil, fmt.Errorf("input schema nesting exceeds 16 levels")
	}
	out := make(map[string]interface{}, len(schema))
	for key, value := range schema {
		switch key {
		case "type":
			kind, ok := value.(string)
			if !ok || !validJSONType(kind) {
				return nil, fmt.Errorf("input schema type is invalid")
			}
			if requireObject && kind != "object" {
				return nil, fmt.Errorf("schema root must be an object")
			}
			out[key] = kind
		case "properties":
			properties, ok := value.(map[string]interface{})
			if !ok || len(properties) > 128 {
				return nil, fmt.Errorf("input schema properties are invalid")
			}
			normalized := make(map[string]interface{}, len(properties))
			for name, raw := range properties {
				if !validIdentifier(name, 128) {
					return nil, fmt.Errorf("input schema property %q is invalid", name)
				}
				child, ok := raw.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("input schema property %q must be an object", name)
				}
				item, err := normalizeSchemaNode(child, depth+1, false)
				if err != nil {
					return nil, fmt.Errorf("input schema property %q: %w", name, err)
				}
				normalized[name] = item
			}
			out[key] = normalized
		case "required":
			values, ok := value.([]interface{})
			if !ok || len(values) > 128 {
				return nil, fmt.Errorf("input schema required list is invalid")
			}
			required := make([]interface{}, 0, len(values))
			seen := make(map[string]struct{}, len(values))
			for _, raw := range values {
				name, ok := raw.(string)
				if !ok || !validIdentifier(name, 128) {
					return nil, fmt.Errorf("input schema required property is invalid")
				}
				if _, duplicate := seen[name]; duplicate {
					return nil, fmt.Errorf("input schema required property %q is duplicated", name)
				}
				seen[name] = struct{}{}
				required = append(required, name)
			}
			out[key] = required
		case "items":
			child, ok := value.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("input schema items must be an object")
			}
			item, err := normalizeSchemaNode(child, depth+1, false)
			if err != nil {
				return nil, err
			}
			out[key] = item
		case "additionalProperties":
			switch typed := value.(type) {
			case bool:
				out[key] = typed
			case map[string]interface{}:
				item, err := normalizeSchemaNode(typed, depth+1, false)
				if err != nil {
					return nil, err
				}
				out[key] = item
			default:
				return nil, fmt.Errorf("input schema additionalProperties is invalid")
			}
		default:
			return nil, fmt.Errorf("unsupported learned input schema keyword %q", key)
		}
	}
	if requireObject {
		if kind, _ := out["type"].(string); kind == "" {
			out["type"] = "object"
		}
	}
	if required, ok := out["required"].([]interface{}); ok {
		properties, _ := out["properties"].(map[string]interface{})
		for _, raw := range required {
			name := raw.(string)
			if _, exists := properties[name]; !exists {
				return nil, fmt.Errorf("required input property %q is not declared", name)
			}
		}
	}
	return out, nil
}

func schemaPathExists(schema map[string]interface{}, path []string) bool {
	if len(schema) == 0 {
		return false
	}
	if len(path) == 0 {
		return true
	}
	current := schema
	for index, segment := range path {
		kind, _ := current["type"].(string)
		if kind == "array" {
			if !numericSegment(segment) {
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
		} else {
			child, ok := raw.(map[string]interface{})
			if !ok {
				return false
			}
			current = child
		}
		if index == len(path)-1 {
			return true
		}
	}
	return true
}

func numericSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validJSONType(value string) bool {
	switch value {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func candidateToolSchema(allowLiterals bool) map[string]interface{} {
	valueProperties := map[string]interface{}{
		"ref": map[string]interface{}{"type": "string", "maxLength": 512},
	}
	if allowLiterals {
		valueProperties["literal"] = map[string]interface{}{}
	}
	value := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": valueProperties,
	}
	condition := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"required": []interface{}{"left", "operator"},
		"properties": map[string]interface{}{
			"left": value,
			"operator": map[string]interface{}{
				"type": "string",
				"enum": []interface{}{"eq", "ne", "gt", "gte", "lt", "lte", "exists", "not_exists"},
			},
			"right": value,
		},
	}
	evidence := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"required": []interface{}{"demonstration_id", "event_ids"},
		"properties": map[string]interface{}{
			"demonstration_id": map[string]interface{}{"type": "string", "maxLength": 256},
			"event_ids": map[string]interface{}{
				"type": "array", "minItems": 1, "maxItems": 64,
				"items": map[string]interface{}{"type": "string", "maxLength": 256},
			},
		},
	}
	step := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"required": []interface{}{"id", "kind"},
		"properties": map[string]interface{}{
			"id":   map[string]interface{}{"type": "string", "maxLength": 128},
			"name": map[string]interface{}{"type": "string", "maxLength": 256},
			"kind": map[string]interface{}{
				"type": "string", "enum": []interface{}{"tool", "validation", "approval"},
			},
			"depends_on": map[string]interface{}{
				"type": "array", "maxItems": 64,
				"items": map[string]interface{}{"type": "string", "maxLength": 128},
			},
			"when": condition,
			"evidence": map[string]interface{}{
				"type": "array", "maxItems": 64, "items": evidence,
			},
			"tool": map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"required": []interface{}{"name"},
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "maxLength": 128},
					"arguments": map[string]interface{}{
						"type": "object", "additionalProperties": value,
					},
					"idempotency_key": value,
				},
			},
			"validation": map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"required": []interface{}{"condition"},
				"properties": map[string]interface{}{
					"condition": condition,
					"message":   map[string]interface{}{"type": "string", "maxLength": 1024},
				},
			},
			"approval": map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"required": []interface{}{"summary", "risk"},
				"properties": map[string]interface{}{
					"summary": map[string]interface{}{"type": "string", "maxLength": 1024},
					"risk": map[string]interface{}{
						"type": "string", "enum": []interface{}{"low", "medium", "high", "critical"},
					},
				},
			},
			"retry": map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"properties": map[string]interface{}{
					"max_attempts": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 3},
					"backoff":      map[string]interface{}{"type": "string", "maxLength": 32},
				},
			},
			"timeout": map[string]interface{}{"type": "string", "maxLength": 32},
		},
	}
	return map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"required": []interface{}{"steps"},
		"properties": map[string]interface{}{
			"description": map[string]interface{}{"type": "string", "maxLength": 4096},
			"steps": map[string]interface{}{
				"type": "array", "minItems": 1, "maxItems": 128, "items": step,
			},
		},
	}
}
