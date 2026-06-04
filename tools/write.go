package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type WriteTool struct{}

func (WriteTool) Name() string { return "Write" }
func (WriteTool) Description() string {
	return "Creates a new file or overwrites an existing file. Existing files must be Read first. Writes are atomic."
}
func (WriteTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"file_path": map[string]interface{}{"type": "string"}, "content": map[string]interface{}{"type": "string"}},
		"required":   []string{"file_path", "content"},
	}
}
func (WriteTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (WriteTool) ParallelSafe() bool    { return false }
func (t WriteTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	path, ok := asString(raw["file_path"])
	if !ok || strings.TrimSpace(path) == "" {
		return nil, core.NewToolExecutionError(t.Name(), "file_path must be a non-empty string")
	}
	content, ok := asString(raw["content"])
	if !ok {
		return nil, core.NewToolExecutionError(t.Name(), "content must be a string")
	}
	return map[string]interface{}{"file_path": path, "content": content}, nil
}
func (t WriteTool) Summarize(input map[string]interface{}) string {
	return fmt.Sprintf("Write %dB to %s", len([]byte(input["content"].(string))), input["file_path"])
}
func (t WriteTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	path := resolvePath(input["file_path"].(string), ctx.CWD)
	if _, err := os.Stat(path); err == nil && !ctx.FileReadTracker.HasRead(path) {
		return core.ToolResult{Content: "Error: file exists and has not been Read in this session.", Summary: t.Summarize(input), IsError: true}, nil
	}
	if err := atomicWrite(path, []byte(input["content"].(string))); err != nil {
		return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	ctx.FileReadTracker.MarkRead(path)
	rel, _ := filepath.Rel(ctx.CWD, path)
	return core.ToolResult{Content: fmt.Sprintf("wrote %d bytes to %s", len([]byte(input["content"].(string))), rel), Summary: t.Summarize(input)}, nil
}
