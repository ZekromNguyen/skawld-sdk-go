package permissions

import (
	"context"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

type testTool struct {
	name  string
	scope core.ToolScope
}

func (t testTool) Name() string        { return t.name }
func (t testTool) Description() string { return "test tool" }
func (t testTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t testTool) Scope() core.ToolScope { return t.scope }
func (t testTool) ParallelSafe() bool    { return true }
func (t testTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return raw, nil
}
func (t testTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
func (t testTool) Summarize(input map[string]interface{}) string { return t.name }

func TestPermissionModes(t *testing.T) {
	read := testTool{name: "Read", scope: core.ToolScopeRead}
	write := testTool{name: "Write", scope: core.ToolScopeWrite}
	execTool := testTool{name: "Bash", scope: core.ToolScopeExec}

	if got := NewEngine(Options{Mode: core.PermissionModeDefault}).Evaluate(call("1", read, nil)).Decision; got != DecisionAllow {
		t.Fatalf("default read expected allow, got %s", got)
	}
	if got := NewEngine(Options{Mode: core.PermissionModeDefault}).Evaluate(call("1", write, nil)).Decision; got != DecisionAsk {
		t.Fatalf("default write expected ask, got %s", got)
	}
	if got := NewEngine(Options{Mode: core.PermissionModeAcceptEdits}).Evaluate(call("1", write, nil)).Decision; got != DecisionAllow {
		t.Fatalf("acceptEdits write expected allow, got %s", got)
	}
	if got := NewEngine(Options{Mode: core.PermissionModeYolo}).Evaluate(call("1", execTool, map[string]interface{}{"command": "rm -rf tmp"})).Decision; got != DecisionAllow {
		t.Fatalf("yolo exec expected allow, got %s", got)
	}
}

func TestNamedToolArgumentRule(t *testing.T) {
	engine := NewEngine(Options{Rules: []Rule{{
		Kind:     "tool",
		Tool:     "Write",
		Arg:      "file_path",
		Value:    "README.md",
		Decision: DecisionAllow,
	}}})
	tool := testTool{name: "Write", scope: core.ToolScopeWrite}
	got := engine.Evaluate(call("1", tool, map[string]interface{}{"file_path": "README.md"})).Decision
	if got != DecisionAllow {
		t.Fatalf("expected named argument rule to allow, got %s", got)
	}
	got = engine.Evaluate(call("2", tool, map[string]interface{}{"file_path": "TODO.md"})).Decision
	if got != DecisionAsk {
		t.Fatalf("expected unmatched argument rule to fall through to ask, got %s", got)
	}
}

func TestBashCommandRule(t *testing.T) {
	engine := NewEngine(Options{Rules: []Rule{{
		Kind:     "bash",
		Command:  "git status*",
		Decision: DecisionAllow,
	}}})
	tool := testTool{name: "Bash", scope: core.ToolScopeExec}
	got := engine.Evaluate(call("1", tool, map[string]interface{}{"command": "git status --short"})).Decision
	if got != DecisionAllow {
		t.Fatalf("expected bash command rule to allow, got %s", got)
	}
	got = engine.Evaluate(call("2", tool, map[string]interface{}{"command": "git clean -fd"})).Decision
	if got != DecisionAsk {
		t.Fatalf("expected unmatched bash command to ask, got %s", got)
	}
}

func TestCanUseToolUpdatedInputValidation(t *testing.T) {
	tool := validatingTool{name: "Write", scope: core.ToolScopeWrite}
	engine := NewEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			return CanUseToolResponse{Behavior: "allow", UpdatedInput: map[string]interface{}{"bad": true}}, nil
		},
	})
	got := engine.Resolve(context.Background(), call("1", tool, map[string]interface{}{"ok": true}))
	if got.Decision != DecisionDeny {
		t.Fatalf("expected invalid updated input to be denied, got %s", got.Decision)
	}
}

func TestCanUseToolAllowsValidatedUpdatedInput(t *testing.T) {
	tool := validatingTool{name: "Write", scope: core.ToolScopeWrite}
	engine := NewEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			return CanUseToolResponse{Behavior: "allow", UpdatedInput: map[string]interface{}{"ok": true, "path": "next"}}, nil
		},
	})
	got := engine.Resolve(context.Background(), call("1", tool, map[string]interface{}{"ok": true, "path": "old"}))
	if got.Decision != DecisionAllow {
		t.Fatalf("expected updated input to be allowed, got %s", got.Decision)
	}
	if got.UpdatedInput["path"] != "next" {
		t.Fatalf("expected rewritten path to be returned, got %+v", got.UpdatedInput)
	}
}

func TestCanUseToolInvalidBehaviorDenied(t *testing.T) {
	engine := NewEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			return CanUseToolResponse{Behavior: "later"}, nil
		},
	})
	tool := testTool{name: "Write", scope: core.ToolScopeWrite}
	got := engine.Resolve(context.Background(), call("1", tool, nil))
	if got.Decision != DecisionDeny {
		t.Fatalf("expected invalid callback behavior to be denied, got %s", got.Decision)
	}
}

type validatingTool struct {
	name  string
	scope core.ToolScope
}

func (t validatingTool) Name() string        { return t.name }
func (t validatingTool) Description() string { return "validating tool" }
func (t validatingTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t validatingTool) Scope() core.ToolScope { return t.scope }
func (t validatingTool) ParallelSafe() bool    { return false }
func (t validatingTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	if _, ok := raw["ok"]; !ok {
		return nil, core.NewToolExecutionError(t.name, "missing ok")
	}
	return raw, nil
}
func (t validatingTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
func (t validatingTool) Summarize(input map[string]interface{}) string { return t.name }

func call(id string, tool core.Tool, input map[string]interface{}) PendingCall {
	if input == nil {
		input = map[string]interface{}{}
	}
	return PendingCall{ToolUseID: id, Tool: tool, Input: input, CWD: "."}
}
