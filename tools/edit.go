package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type EditTool struct{}

func (EditTool) Name() string { return "Edit" }
func (EditTool) Description() string {
	return "Performs exact string replacement in a file. The file must be Read first. old_string must be unique unless replace_all is true."
}
func (EditTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path":   map[string]interface{}{"type": "string"},
			"old_string":  map[string]interface{}{"type": "string"},
			"new_string":  map[string]interface{}{"type": "string"},
			"replace_all": map[string]interface{}{"type": "boolean"},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}
func (EditTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (EditTool) ParallelSafe() bool    { return false }
func (t EditTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseEditInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t EditTool) Summarize(input map[string]interface{}) string {
	mode := "replace one"
	if asBool(input["replace_all"]) {
		mode = "replace all"
	}
	return fmt.Sprintf("Edit %s (%s)", input["file_path"], mode)
}
func (t EditTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	path, err := resolveFilesystem(ctx, input["file_path"].(string), core.FilesystemResolveWrite)
	if err != nil {
		return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	if !ctx.FileReadTracker.HasRead(path) {
		return core.ToolResult{Content: "Error: You must Read this file before editing it.", Summary: t.Summarize(input), IsError: true}, nil
	}
	oldString := input["old_string"].(string)
	newString := input["new_string"].(string)
	if oldString == newString {
		return core.ToolResult{Content: "Error: No changes to make.", Summary: t.Summarize(input), IsError: true}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	text := string(data)
	count := strings.Count(text, oldString)
	if count == 0 {
		return core.ToolResult{Content: "Error: old_string not found in file.", Summary: t.Summarize(input), IsError: true}, nil
	}
	if count > 1 && !asBool(input["replace_all"]) {
		return core.ToolResult{Content: fmt.Sprintf("Error: old_string matches %d occurrences; pass replace_all or provide more context.", count), Summary: t.Summarize(input), IsError: true}, nil
	}
	lineEnding := detectLineEnding(text[:min(len(text), 16*1024)])
	if lineEnding == "\r\n" {
		newString = strings.ReplaceAll(strings.ReplaceAll(newString, "\r\n", "\n"), "\n", "\r\n")
	}
	next := strings.Replace(text, oldString, newString, 1)
	if asBool(input["replace_all"]) {
		next = strings.ReplaceAll(text, oldString, newString)
	}
	if err := atomicWrite(path, []byte(next)); err != nil {
		return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	rel, _ := filepath.Rel(ctx.CWD, path)
	return core.ToolResult{Content: fmt.Sprintf("Edited %s: %+d lines", rel, strings.Count(next, "\n")-strings.Count(text, "\n")), Summary: t.Summarize(input)}, nil
}
