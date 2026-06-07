package tools

import (
	"encoding/json"
	"fmt"

	"github.com/skawld/skawld-sdk-go/core"
)

type TaskCreateTool struct{}
type TaskListTool struct{}
type TaskGetTool struct{}
type TaskUpdateTool struct{}

func (TaskCreateTool) Name() string { return "TaskCreate" }
func (TaskCreateTool) Description() string {
	return "Create a new persistent task in the current session."
}
func (TaskCreateTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (TaskCreateTool) ParallelSafe() bool    { return true }
func (TaskCreateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"subject": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}, "active_form": map[string]interface{}{"type": "string"}, "metadata": map[string]interface{}{"type": "object"}}, "required": []string{"subject", "description"}}
}
func (t TaskCreateTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	subject, ok := asString(raw["subject"])
	if !ok || subject == "" {
		return nil, core.NewToolExecutionError(t.Name(), "subject is required")
	}
	desc, ok := asString(raw["description"])
	if !ok {
		return nil, core.NewToolExecutionError(t.Name(), "description is required")
	}
	out := map[string]interface{}{"subject": subject, "description": desc}
	if s, ok := asString(raw["active_form"]); ok {
		out["active_form"] = s
	}
	if m, ok := raw["metadata"].(map[string]interface{}); ok {
		out["metadata"] = m
	}
	return out, nil
}
func (t TaskCreateTool) Summarize(input map[string]interface{}) string {
	return "Create task: " + input["subject"].(string)
}
func (t TaskCreateTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := core.CreateTaskInput{Subject: input["subject"].(string), Description: input["description"].(string)}
	if s, ok := asString(input["active_form"]); ok {
		in.ActiveForm = s
	}
	if m, ok := input["metadata"].(map[string]interface{}); ok {
		in.Metadata = m
	}
	task, err := ctx.SessionStore.CreateTask(ctx.Context, ctx.SessionID, in)
	if err != nil {
		return core.ToolResult{Content: err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	return core.ToolResult{Content: fmt.Sprintf("Task #%s created: %s", task.ID, task.Subject), Summary: t.Summarize(input)}, nil
}

func (TaskListTool) Name() string          { return "TaskList" }
func (TaskListTool) Description() string   { return "List persistent tasks in the current session." }
func (TaskListTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (TaskListTool) ParallelSafe() bool    { return true }
func (TaskListTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (TaskListTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (TaskListTool) Summarize(input map[string]interface{}) string { return "List tasks" }
func (t TaskListTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	tasks, err := ctx.SessionStore.ListTasks(ctx.Context, ctx.SessionID)
	if err != nil {
		return core.ToolResult{Content: err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	raw, _ := json.MarshalIndent(tasks, "", "  ")
	return core.ToolResult{Content: string(raw), Summary: fmt.Sprintf("%d task(s)", len(tasks))}, nil
}

func (TaskGetTool) Name() string          { return "TaskGet" }
func (TaskGetTool) Description() string   { return "Get one persistent task by id." }
func (TaskGetTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (TaskGetTool) ParallelSafe() bool    { return true }
func (TaskGetTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}, "required": []string{"id"}}
}
func (t TaskGetTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	id, ok := asString(raw["id"])
	if !ok || id == "" {
		return nil, core.NewToolExecutionError(t.Name(), "id is required")
	}
	return map[string]interface{}{"id": id}, nil
}
func (TaskGetTool) Summarize(input map[string]interface{}) string {
	return "Get task #" + input["id"].(string)
}
func (t TaskGetTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	task, ok, err := ctx.SessionStore.GetTask(ctx.Context, ctx.SessionID, input["id"].(string))
	if err != nil {
		return core.ToolResult{Content: err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	if !ok {
		return core.ToolResult{Content: "Task not found.", Summary: t.Summarize(input), IsError: true}, nil
	}
	raw, _ := json.MarshalIndent(task, "", "  ")
	return core.ToolResult{Content: string(raw), Summary: t.Summarize(input)}, nil
}

func (TaskUpdateTool) Name() string          { return "TaskUpdate" }
func (TaskUpdateTool) Description() string   { return "Update a persistent task in the current session." }
func (TaskUpdateTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (TaskUpdateTool) ParallelSafe() bool    { return true }
func (TaskUpdateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id":                map[string]interface{}{"type": "string"},
			"status":            map[string]interface{}{"type": "string", "enum": []string{"pending", "in_progress", "completed", "deleted"}},
			"subject":           map[string]interface{}{"type": "string"},
			"description":       map[string]interface{}{"type": "string"},
			"active_form":       map[string]interface{}{"type": "string"},
			"owner":             map[string]interface{}{"type": "string"},
			"metadata":          map[string]interface{}{"type": "object", "description": "Metadata patch. Null values delete keys."},
			"add_blocks":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"add_blocked_by":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"remove_blocks":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"remove_blocked_by": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"delete":            map[string]interface{}{"type": "boolean"},
		},
		"required": []string{"id"},
	}
}
func (t TaskUpdateTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	id, ok := asString(raw["id"])
	if !ok || id == "" {
		return nil, core.NewToolExecutionError(t.Name(), "id is required")
	}
	out := map[string]interface{}{"id": id}
	for _, key := range []string{"subject", "description", "active_form", "owner"} {
		if s, ok := asString(raw[key]); ok {
			out[key] = s
		}
	}
	if status, ok := asString(raw["status"]); ok && status != "" {
		st, err := parseTaskStatus(status)
		if err != nil {
			return nil, core.NewToolExecutionError(t.Name(), err.Error())
		}
		out["status"] = string(st)
	}
	if metadata, exists := raw["metadata"]; exists {
		m, ok := metadata.(map[string]interface{})
		if !ok {
			return nil, core.NewToolExecutionError(t.Name(), "metadata must be an object")
		}
		out["metadata"] = m
	}
	for _, key := range []string{"add_blocks", "add_blocked_by", "remove_blocks", "remove_blocked_by"} {
		ids, ok, err := stringList(raw, key)
		if err != nil {
			return nil, core.NewToolExecutionError(t.Name(), err.Error())
		}
		if ok {
			out[key] = ids
		}
	}
	if v, exists := raw["delete"]; exists {
		b, ok := v.(bool)
		if !ok {
			return nil, core.NewToolExecutionError(t.Name(), "delete must be a boolean")
		}
		out["delete"] = b
	}
	return out, nil
}
func (TaskUpdateTool) Summarize(input map[string]interface{}) string {
	return "Update task #" + input["id"].(string)
}
func (t TaskUpdateTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	var patch core.TaskPatch
	if s, ok := asString(input["subject"]); ok {
		patch.Subject = &s
	}
	if s, ok := asString(input["description"]); ok {
		patch.Description = &s
	}
	if s, ok := asString(input["active_form"]); ok {
		patch.ActiveForm = &s
	}
	if s, ok := asString(input["owner"]); ok {
		patch.Owner = &s
	}
	if s, ok := asString(input["status"]); ok {
		st := core.TaskStatus(s)
		patch.Status = &st
	}
	if m, ok := input["metadata"].(map[string]interface{}); ok {
		patch.Metadata = m
	}
	if ids, ok := input["add_blocks"].([]string); ok {
		patch.AddBlocks = ids
	}
	if ids, ok := input["add_blocked_by"].([]string); ok {
		patch.AddBlockedBy = ids
	}
	if ids, ok := input["remove_blocks"].([]string); ok {
		patch.RemoveBlocks = ids
	}
	if ids, ok := input["remove_blocked_by"].([]string); ok {
		patch.RemoveBlockedBy = ids
	}
	if del, ok := input["delete"].(bool); ok {
		patch.Delete = del
	}
	task, ok, err := ctx.SessionStore.UpdateTask(ctx.Context, ctx.SessionID, input["id"].(string), patch)
	if err != nil {
		return core.ToolResult{Content: err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	if !ok {
		return core.ToolResult{Content: "Task not found.", Summary: t.Summarize(input), IsError: true}, nil
	}
	raw, _ := json.MarshalIndent(task, "", "  ")
	return core.ToolResult{Content: string(raw), Summary: t.Summarize(input)}, nil
}

func parseTaskStatus(status string) (core.TaskStatus, error) {
	st := core.TaskStatus(status)
	switch st {
	case core.TaskPending, core.TaskInProgress, core.TaskCompleted, core.TaskDeleted:
		return st, nil
	default:
		return "", fmt.Errorf("status must be one of: pending, in_progress, completed, deleted")
	}
}

func stringList(raw map[string]interface{}, key string) ([]string, bool, error) {
	v, exists := raw[key]
	if !exists {
		return nil, false, nil
	}
	switch vals := v.(type) {
	case []string:
		return append([]string(nil), vals...), true, nil
	case []interface{}:
		out := make([]string, 0, len(vals))
		for _, item := range vals {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, false, fmt.Errorf("%s must contain non-empty strings", key)
			}
			out = append(out, s)
		}
		return out, true, nil
	default:
		return nil, false, fmt.Errorf("%s must be an array of strings", key)
	}
}
