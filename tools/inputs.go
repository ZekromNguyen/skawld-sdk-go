package tools

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type readInput struct {
	FilePath string
	Offset   int
	Limit    int
}

func parseReadInput(raw map[string]interface{}) (readInput, error) {
	path, ok := asString(raw["file_path"])
	if !ok || strings.TrimSpace(path) == "" {
		return readInput{}, core.NewToolExecutionError("Read", "file_path must be a non-empty string")
	}
	return readInput{
		FilePath: path,
		Offset:   max(1, asInt(raw["offset"], 1)),
		Limit:    max(1, asInt(raw["limit"], 2000)),
	}, nil
}

func readInputFrom(input map[string]interface{}) readInput {
	parsed, _ := parseReadInput(input)
	return parsed
}

func (in readInput) mapValue() map[string]interface{} {
	return map[string]interface{}{"file_path": in.FilePath, "offset": in.Offset, "limit": in.Limit}
}

type writeInput struct {
	FilePath string
	Content  string
}

func parseWriteInput(raw map[string]interface{}) (writeInput, error) {
	path, ok := asString(raw["file_path"])
	if !ok || strings.TrimSpace(path) == "" {
		return writeInput{}, core.NewToolExecutionError("Write", "file_path must be a non-empty string")
	}
	content, ok := asString(raw["content"])
	if !ok {
		return writeInput{}, core.NewToolExecutionError("Write", "content must be a string")
	}
	return writeInput{FilePath: path, Content: content}, nil
}

func writeInputFrom(input map[string]interface{}) writeInput {
	parsed, _ := parseWriteInput(input)
	return parsed
}

func (in writeInput) mapValue() map[string]interface{} {
	return map[string]interface{}{"file_path": in.FilePath, "content": in.Content}
}

type editInput struct {
	FilePath   string
	OldString  string
	NewString  string
	ReplaceAll bool
}

func parseEditInput(raw map[string]interface{}) (editInput, error) {
	path, ok := asString(raw["file_path"])
	if !ok || strings.TrimSpace(path) == "" {
		return editInput{}, core.NewToolExecutionError("Edit", "file_path must be a non-empty string")
	}
	oldString, ok := asString(raw["old_string"])
	if !ok {
		return editInput{}, core.NewToolExecutionError("Edit", "old_string must be a string")
	}
	newString, ok := asString(raw["new_string"])
	if !ok {
		return editInput{}, core.NewToolExecutionError("Edit", "new_string must be a string")
	}
	return editInput{
		FilePath:   path,
		OldString:  oldString,
		NewString:  newString,
		ReplaceAll: asBool(raw["replace_all"]),
	}, nil
}

func editInputFrom(input map[string]interface{}) editInput {
	parsed, _ := parseEditInput(input)
	return parsed
}

func (in editInput) mapValue() map[string]interface{} {
	return map[string]interface{}{"file_path": in.FilePath, "old_string": in.OldString, "new_string": in.NewString, "replace_all": in.ReplaceAll}
}

type globInput struct {
	Pattern string
	Path    string
}

func parseGlobInput(raw map[string]interface{}) (globInput, error) {
	pattern, ok := asString(raw["pattern"])
	if !ok || pattern == "" {
		return globInput{}, core.NewToolExecutionError("Glob", "pattern must be a non-empty string")
	}
	out := globInput{Pattern: filepath.ToSlash(pattern)}
	if p, ok := asString(raw["path"]); ok {
		out.Path = p
	}
	return out, nil
}

func globInputFrom(input map[string]interface{}) globInput {
	parsed, _ := parseGlobInput(input)
	return parsed
}

func (in globInput) mapValue() map[string]interface{} {
	out := map[string]interface{}{"pattern": in.Pattern}
	if in.Path != "" {
		out["path"] = in.Path
	}
	return out
}

type grepInput struct {
	Pattern    string
	Path       string
	Glob       string
	Type       string
	OutputMode string
	IgnoreCase bool
	LineNumber bool
	After      *int
	Before     *int
	Context    *int
	Multiline  bool
	HeadLimit  int
}

func parseGrepInput(raw map[string]interface{}) (grepInput, error) {
	pattern, ok := asString(raw["pattern"])
	if !ok || pattern == "" {
		return grepInput{}, core.NewToolExecutionError("Grep", "pattern is required and must be a non-empty string")
	}
	out := grepInput{Pattern: pattern, OutputMode: "files_with_matches", HeadLimit: 250}
	if mode, ok := asString(raw["output_mode"]); ok && mode != "" {
		switch mode {
		case "files_with_matches", "content", "count":
			out.OutputMode = mode
		default:
			return grepInput{}, core.NewToolExecutionError("Grep", "output_mode must be one of: files_with_matches, content, count")
		}
	}
	if p, ok := asString(raw["path"]); ok {
		out.Path = p
	}
	if g, ok := asString(raw["glob"]); ok {
		out.Glob = filepath.ToSlash(g)
	}
	if typ, ok := asString(raw["type"]); ok {
		out.Type = typ
	}
	out.IgnoreCase = asBool(raw["-i"])
	out.LineNumber = asBool(raw["-n"])
	out.Multiline = asBool(raw["multiline"])
	if a, ok := coerceNonNegativeInt(raw, "-A"); ok && a >= 0 {
		out.After = intPtr(a)
	}
	if b, ok := coerceNonNegativeInt(raw, "-B"); ok && b >= 0 {
		out.Before = intPtr(b)
	}
	if c, ok := coerceNonNegativeInt(raw, "-C"); ok && c >= 0 {
		out.Context = intPtr(c)
	}
	if hl, ok := coerceNonNegativeInt(raw, "head_limit"); ok && hl >= 0 {
		out.HeadLimit = hl
	}
	return out, nil
}

func grepInputFrom(input map[string]interface{}) grepInput {
	parsed, _ := parseGrepInput(input)
	return parsed
}

func (in grepInput) mapValue() map[string]interface{} {
	out := map[string]interface{}{
		"pattern":     in.Pattern,
		"output_mode": in.OutputMode,
		"-i":          in.IgnoreCase,
		"-n":          in.LineNumber,
		"multiline":   in.Multiline,
		"head_limit":  in.HeadLimit,
	}
	if in.Path != "" {
		out["path"] = in.Path
	}
	if in.Glob != "" {
		out["glob"] = in.Glob
	}
	if in.Type != "" {
		out["type"] = in.Type
	}
	if in.After != nil {
		out["-A"] = *in.After
	}
	if in.Before != nil {
		out["-B"] = *in.Before
	}
	if in.Context != nil {
		out["-C"] = *in.Context
	}
	return out
}

type bashInput struct {
	Command     string
	TimeoutMS   int
	Description string
}

func parseBashInput(raw map[string]interface{}) (bashInput, error) {
	command, ok := asString(raw["command"])
	if !ok || strings.TrimSpace(command) == "" {
		return bashInput{}, core.NewToolExecutionError("Bash", "Bash: 'command' must be a non-empty string.")
	}
	timeout := 120000
	if tval, ok := coerceNonNegativeInt(raw, "timeout_ms"); ok {
		timeout = tval
	}
	if timeout < 100 {
		timeout = 100
	}
	if timeout > 1800000 {
		timeout = 1800000
	}
	out := bashInput{Command: command, TimeoutMS: timeout}
	if desc, ok := asString(raw["description"]); ok {
		out.Description = desc
	}
	return out, nil
}

func bashInputFrom(input map[string]interface{}) bashInput {
	parsed, _ := parseBashInput(input)
	return parsed
}

func (in bashInput) mapValue() map[string]interface{} {
	out := map[string]interface{}{"command": in.Command, "timeout_ms": in.TimeoutMS}
	if in.Description != "" {
		out["description"] = in.Description
	}
	return out
}

type taskCreateInput struct {
	Subject     string
	Description string
	ActiveForm  string
	Metadata    map[string]interface{}
}

func parseTaskCreateInput(raw map[string]interface{}) (taskCreateInput, error) {
	subject, ok := asString(raw["subject"])
	if !ok || subject == "" {
		return taskCreateInput{}, core.NewToolExecutionError("TaskCreate", "subject is required")
	}
	desc, ok := asString(raw["description"])
	if !ok {
		return taskCreateInput{}, core.NewToolExecutionError("TaskCreate", "description is required")
	}
	out := taskCreateInput{Subject: subject, Description: desc}
	if s, ok := asString(raw["active_form"]); ok {
		out.ActiveForm = s
	}
	if m, ok := raw["metadata"].(map[string]interface{}); ok {
		out.Metadata = m
	}
	return out, nil
}

func taskCreateInputFrom(input map[string]interface{}) taskCreateInput {
	parsed, _ := parseTaskCreateInput(input)
	return parsed
}

func (in taskCreateInput) mapValue() map[string]interface{} {
	out := map[string]interface{}{"subject": in.Subject, "description": in.Description}
	if in.ActiveForm != "" {
		out["active_form"] = in.ActiveForm
	}
	if in.Metadata != nil {
		out["metadata"] = in.Metadata
	}
	return out
}

type taskGetInput struct {
	ID string
}

func parseTaskGetInput(raw map[string]interface{}, toolName string) (taskGetInput, error) {
	id, ok := asString(raw["id"])
	if !ok || id == "" {
		return taskGetInput{}, core.NewToolExecutionError(toolName, "id is required")
	}
	return taskGetInput{ID: id}, nil
}

func taskGetInputFrom(input map[string]interface{}) taskGetInput {
	parsed, _ := parseTaskGetInput(input, "TaskGet")
	return parsed
}

func (in taskGetInput) mapValue() map[string]interface{} {
	return map[string]interface{}{"id": in.ID}
}

type taskUpdateInput struct {
	ID              string
	Subject         *string
	Description     *string
	ActiveForm      *string
	Owner           *string
	Status          *core.TaskStatus
	Metadata        map[string]interface{}
	AddBlocks       []string
	AddBlockedBy    []string
	RemoveBlocks    []string
	RemoveBlockedBy []string
	Delete          *bool
}

func parseTaskUpdateInput(raw map[string]interface{}) (taskUpdateInput, error) {
	id, ok := asString(raw["id"])
	if !ok || id == "" {
		return taskUpdateInput{}, core.NewToolExecutionError("TaskUpdate", "id is required")
	}
	out := taskUpdateInput{ID: id}
	for _, key := range []string{"subject", "description", "active_form", "owner"} {
		if s, ok := asString(raw[key]); ok {
			out.setString(key, s)
		}
	}
	if status, ok := asString(raw["status"]); ok && status != "" {
		st, err := parseTaskStatus(status)
		if err != nil {
			return taskUpdateInput{}, core.NewToolExecutionError("TaskUpdate", err.Error())
		}
		out.Status = &st
	}
	if metadata, exists := raw["metadata"]; exists {
		m, ok := metadata.(map[string]interface{})
		if !ok {
			return taskUpdateInput{}, core.NewToolExecutionError("TaskUpdate", "metadata must be an object")
		}
		out.Metadata = m
	}
	for _, key := range []string{"add_blocks", "add_blocked_by", "remove_blocks", "remove_blocked_by"} {
		ids, ok, err := stringList(raw, key)
		if err != nil {
			return taskUpdateInput{}, core.NewToolExecutionError("TaskUpdate", err.Error())
		}
		if ok {
			out.setIDs(key, ids)
		}
	}
	if v, exists := raw["delete"]; exists {
		b, ok := v.(bool)
		if !ok {
			return taskUpdateInput{}, core.NewToolExecutionError("TaskUpdate", "delete must be a boolean")
		}
		out.Delete = &b
	}
	return out, nil
}

func taskUpdateInputFrom(input map[string]interface{}) taskUpdateInput {
	parsed, _ := parseTaskUpdateInput(input)
	return parsed
}

func (in *taskUpdateInput) setString(key, value string) {
	switch key {
	case "subject":
		in.Subject = &value
	case "description":
		in.Description = &value
	case "active_form":
		in.ActiveForm = &value
	case "owner":
		in.Owner = &value
	}
}

func (in *taskUpdateInput) setIDs(key string, ids []string) {
	switch key {
	case "add_blocks":
		in.AddBlocks = ids
	case "add_blocked_by":
		in.AddBlockedBy = ids
	case "remove_blocks":
		in.RemoveBlocks = ids
	case "remove_blocked_by":
		in.RemoveBlockedBy = ids
	}
}

func (in taskUpdateInput) mapValue() map[string]interface{} {
	out := map[string]interface{}{"id": in.ID}
	if in.Subject != nil {
		out["subject"] = *in.Subject
	}
	if in.Description != nil {
		out["description"] = *in.Description
	}
	if in.ActiveForm != nil {
		out["active_form"] = *in.ActiveForm
	}
	if in.Owner != nil {
		out["owner"] = *in.Owner
	}
	if in.Status != nil {
		out["status"] = string(*in.Status)
	}
	if in.Metadata != nil {
		out["metadata"] = in.Metadata
	}
	if in.AddBlocks != nil {
		out["add_blocks"] = in.AddBlocks
	}
	if in.AddBlockedBy != nil {
		out["add_blocked_by"] = in.AddBlockedBy
	}
	if in.RemoveBlocks != nil {
		out["remove_blocks"] = in.RemoveBlocks
	}
	if in.RemoveBlockedBy != nil {
		out["remove_blocked_by"] = in.RemoveBlockedBy
	}
	if in.Delete != nil {
		out["delete"] = *in.Delete
	}
	return out
}

func (in taskUpdateInput) patch() core.TaskPatch {
	patch := core.TaskPatch{
		Subject:         in.Subject,
		Description:     in.Description,
		ActiveForm:      in.ActiveForm,
		Owner:           in.Owner,
		Status:          in.Status,
		Metadata:        in.Metadata,
		AddBlocks:       in.AddBlocks,
		AddBlockedBy:    in.AddBlockedBy,
		RemoveBlocks:    in.RemoveBlocks,
		RemoveBlockedBy: in.RemoveBlockedBy,
	}
	if in.Delete != nil {
		patch.Delete = *in.Delete
	}
	return patch
}

func coerceNonNegativeInt(raw map[string]interface{}, key string) (int, bool) {
	if val, ok := raw[key]; ok {
		switch n := val.(type) {
		case float64:
			return int(n), true
		case int:
			return n, true
		case string:
			i, _ := strconv.Atoi(n)
			return i, true
		}
	}
	return -1, false
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

func intPtr(value int) *int {
	return &value
}
