package subagents

import (
	"fmt"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type Invocation struct {
	Agent string
	Task  string
}

type Tool struct {
	Registry *Registry
}

func (Tool) Name() string { return "Subagent" }
func (Tool) Description() string {
	return "Run a delegated task in a child agent and return its final result."
}
func (Tool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (Tool) ParallelSafe() bool    { return false }
func (Tool) InputSchema() map[string]interface{} {
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
func (t Tool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	task, ok := raw["task"].(string)
	if !ok || strings.TrimSpace(task) == "" {
		return nil, core.NewToolExecutionError(t.Name(), "task is required")
	}
	agent, _ := raw["agent"].(string)
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = "default"
	}
	if t.Registry != nil {
		if _, ok := t.Registry.Get(agent); !ok {
			return nil, core.NewToolExecutionError(t.Name(), fmt.Sprintf("subagent %q is not loaded", agent))
		}
	}
	return map[string]interface{}{"agent": agent, "task": strings.TrimSpace(task)}, nil
}
func (Tool) Summarize(input map[string]interface{}) string {
	agent, _ := input["agent"].(string)
	return "Run subagent " + agent
}
func (t Tool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	if ctx.RunSubagent == nil {
		return core.ToolResult{Content: "Subagent runner is not configured.", Summary: t.Summarize(input), IsError: true}, nil
	}
	return ctx.RunSubagent(ctx.Context, core.SubagentInvocation{
		Name: input["agent"].(string),
		Task: input["task"].(string),
	})
}
