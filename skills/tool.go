package skills

import (
	"fmt"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type Tool struct {
	Manager *Manager
}

func (Tool) Name() string { return "Skill" }
func (Tool) Description() string {
	return "Invoke a loaded SKILL.md skill for the next assistant turn."
}
func (Tool) Scope() core.ToolScope { return core.ToolScopeRead }
func (Tool) ParallelSafe() bool    { return false }
func (Tool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskLow, SideEffect: core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
		Timeout:     5 * time.Second,
		Permissions: []string{"skill.invoke"},
		OutputSchema: map[string]interface{}{
			"type": "string",
		},
	}
}
func (Tool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill":     map[string]interface{}{"type": "string"},
			"arguments": map[string]interface{}{"type": "string"},
		},
		"required": []string{"skill"},
	}
}
func (t Tool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	name, ok := raw["skill"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, core.NewToolExecutionError(t.Name(), "skill is required")
	}
	name = strings.TrimSpace(name)
	if t.Manager != nil {
		if _, ok := t.Manager.Get(name); !ok {
			return nil, core.NewToolExecutionError(t.Name(), fmt.Sprintf("skill %q is not loaded", name))
		}
	}
	args, _ := raw["arguments"].(string)
	return map[string]interface{}{"skill": name, "arguments": args}, nil
}
func (Tool) Summarize(input map[string]interface{}) string {
	return "Invoke skill " + input["skill"].(string)
}
func (t Tool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	if ctx.InvokeSkill == nil {
		return core.ToolResult{Content: "Skill invocation is not configured.", Summary: t.Summarize(input), IsError: true}, nil
	}
	return ctx.InvokeSkill(ctx.Context, core.SkillInvocation{
		Name:      input["skill"].(string),
		Arguments: input["arguments"].(string),
	})
}
