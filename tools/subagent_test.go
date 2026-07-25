package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestSubagentToolDefaultsAgentAndRunsContextRunner(t *testing.T) {
	tool := SubagentTool{}
	input, err := tool.Validate(map[string]interface{}{"task": "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if input["agent"] != "default" || input["task"] != "inspect" {
		t.Fatalf("unexpected input: %+v", input)
	}
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

func TestSubagentToolRequiresRunner(t *testing.T) {
	tool := SubagentTool{}
	input, err := tool.Validate(map[string]interface{}{"agent": "review", "task": "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content.(string), "not configured") {
		t.Fatalf("expected missing runner error, got %+v", result)
	}
}
