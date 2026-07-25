package observation

import (
	"fmt"
	"strings"
)

type RedactionAction string

const (
	RedactDrop RedactionAction = "drop"
	RedactMask RedactionAction = "mask"
)

// Sanitizer is the trusted ingress boundary for minimizing captured payloads.
// Rules are deterministic application policy, not instructions inferred from
// observed content or a model.
type Sanitizer interface {
	SanitizeEvent(Event) (Event, error)
	SanitizeMap(string, map[string]interface{}) (map[string]interface{}, error)
}

type RedactorOptions struct {
	// Rules use exact dotted paths such as input.password or
	// initial_context.access_token. A trailing .* removes or masks every direct
	// and nested value under that object.
	Rules       map[string]RedactionAction
	Replacement string
}

type Redactor struct {
	rules       map[string]RedactionAction
	replacement string
}

func NewRedactor(options RedactorOptions) (*Redactor, error) {
	redactor := &Redactor{
		rules:       make(map[string]RedactionAction, len(options.Rules)),
		replacement: options.Replacement,
	}
	if redactor.replacement == "" {
		redactor.replacement = "[REDACTED]"
	}
	for rawPath, action := range options.Rules {
		path := strings.TrimSpace(rawPath)
		if !validRedactionPath(path) {
			return nil, fmt.Errorf("invalid observation redaction path %q", rawPath)
		}
		switch action {
		case RedactDrop, RedactMask:
		default:
			return nil, fmt.Errorf(
				"invalid observation redaction action %q", action,
			)
		}
		redactor.rules[path] = action
	}
	return redactor, nil
}

func (r *Redactor) SanitizeEvent(event Event) (Event, error) {
	cloned, err := cloneEvent(event)
	if err != nil {
		return Event{}, err
	}
	fields := []struct {
		scope string
		value *map[string]interface{}
	}{
		{"input", &cloned.Input},
		{"output", &cloned.Output},
		{"context", &cloned.Context},
		{"decision", &cloned.Decision},
		{"result", &cloned.Result},
	}
	for _, field := range fields {
		sanitized, err := r.SanitizeMap(field.scope, *field.value)
		if err != nil {
			return Event{}, err
		}
		*field.value = sanitized
	}
	return cloned, nil
}

func (r *Redactor) SanitizeMap(
	scope string,
	value map[string]interface{},
) (map[string]interface{}, error) {
	cloned := cloneMap(value)
	for path, action := range r.rules {
		parts := strings.Split(path, ".")
		if len(parts) < 2 || parts[0] != scope {
			continue
		}
		applyRedaction(cloned, parts[1:], action, r.replacement)
	}
	return cloned, nil
}

func applyRedaction(
	current map[string]interface{},
	path []string,
	action RedactionAction,
	replacement string,
) {
	if len(path) == 0 || current == nil {
		return
	}
	if len(path) == 1 {
		if path[0] == "*" {
			for key := range current {
				if action == RedactDrop {
					delete(current, key)
				} else {
					current[key] = replacement
				}
			}
			return
		}
		if _, exists := current[path[0]]; !exists {
			return
		}
		if action == RedactDrop {
			delete(current, path[0])
		} else {
			current[path[0]] = replacement
		}
		return
	}
	child, ok := current[path[0]].(map[string]interface{})
	if !ok {
		return
	}
	applyRedaction(child, path[1:], action, replacement)
}

func validRedactionPath(path string) bool {
	if path == "" || len(path) > 512 ||
		strings.ContainsAny(path, "\r\n\x00") {
		return false
	}
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return false
	}
	for index, part := range parts {
		if part == "" || part == "*" && index != len(parts)-1 {
			return false
		}
	}
	return true
}

func cloneEvent(event Event) (Event, error) {
	demo := Demonstration{Trace: WorkflowTrace{Events: []Event{event}}}
	cloned, err := cloneDemonstration(demo)
	if err != nil {
		return Event{}, err
	}
	return cloned.Trace.Events[0], nil
}

var _ Sanitizer = (*Redactor)(nil)
