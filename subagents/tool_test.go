package subagents

import (
	"context"
	"strings"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestToolValidateDefaultsAgentAndChecksRegistry(t *testing.T) {
	reg := NewRegistry("")
	if err := reg.Load(); err != nil {
		t.Fatal(err)
	}
	tool := Tool{Registry: reg}
	input, err := tool.Validate(map[string]interface{}{"task": "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if input["agent"] != "default" || input["task"] != "inspect" {
		t.Fatalf("unexpected input: %+v", input)
	}
	if _, err := tool.Validate(map[string]interface{}{"agent": "missing", "task": "inspect"}); err == nil {
		t.Fatal("expected missing subagent validation error")
	}
}

func TestToolExecuteUsesContextRunner(t *testing.T) {
	tool := Tool{}
	input := map[string]interface{}{"agent": "default", "task": "inspect"}
	result, err := tool.Execute(input, core.ToolContext{
		Context: context.Background(),
		RunSubagent: func(ctx context.Context, inv core.SubagentInvocation) (core.ToolResult, error) {
			if inv.Name != "default" || inv.Task != "inspect" {
				t.Fatalf("unexpected invocation: %+v", inv)
			}
			return core.ToolResult{Content: "done", Summary: "done"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if summary := tool.Summarize(input); !strings.Contains(summary, "default") {
		t.Fatalf("unexpected summary: %s", summary)
	}
}
