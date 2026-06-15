package tools

import (
	"context"

	"github.com/skawld/skawld-sdk-go/core"
)

type SubagentTool struct{}

func (SubagentTool) Name() string { return "Subagent" }
func (SubagentTool) Description() string {
	return "Run a delegated task in a child agent and return its final result."
}
func (SubagentTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"agent": map[string]interface{}{
				"type":        "string",
				"description": "Subagent name. Defaults to default.",
			},
			"task": map[string]interface{}{
				"type":        "string",
				"description": "Task to delegate to the subagent.",
			},
		},
		"required": []string{"task"},
	}
}
func (SubagentTool) Scope() core.ToolScope { return core.ToolScopeExec }
func (SubagentTool) ParallelSafe() bool    { return false }
func (t SubagentTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseSubagentInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (SubagentTool) Summarize(input map[string]interface{}) string {
	in := subagentInputFrom(input)
	return "Run subagent " + in.Agent
}
func (t SubagentTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := subagentInputFrom(input)
	if ctx.RunSubagent == nil {
		return core.ToolResult{Content: "Subagent runner is not configured.", Summary: t.Summarize(input), IsError: true}, nil
	}
	runCtx := ctx.Context
	if runCtx == nil {
		runCtx = context.Background()
	}
	return ctx.RunSubagent(runCtx, core.SubagentInvocation{Name: in.Agent, Task: in.Task})
}
