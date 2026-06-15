package tools

import (
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestProcessToolValidate(t *testing.T) {
	tool := ProcessTool{}

	// Missing action
	_, err := tool.Validate(map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing action")
	}

	// Invalid action
	_, err = tool.Validate(map[string]interface{}{"action": "invalid"})
	if err == nil {
		t.Error("expected error for invalid action")
	}

	// Valid actions
	for _, action := range []string{"list", "poll", "logs", "kill", "spawn"} {
		input := map[string]interface{}{"action": action}
		if action == "poll" || action == "logs" || action == "kill" {
			input["pid"] = "bg-1"
		}
		if action == "spawn" {
			input["command"] = "sleep 1"
		}
		result, err := tool.Validate(input)
		if err != nil {
			t.Errorf("unexpected error for action %q: %v", action, err)
		}
		if result["action"] != action {
			t.Errorf("action = %v, want %v", result["action"], action)
		}
	}

	// Poll without pid
	_, err = tool.Validate(map[string]interface{}{"action": "poll"})
	if err == nil {
		t.Error("expected error for poll without pid")
	}

	// Spawn without command
	_, err = tool.Validate(map[string]interface{}{"action": "spawn"})
	if err == nil {
		t.Error("expected error for spawn without command")
	}
}

func TestProcessSummarize(t *testing.T) {
	tool := ProcessTool{}

	tests := []struct {
		input  map[string]interface{}
		expect string
	}{
		{map[string]interface{}{"action": "list"}, "Process list"},
		{map[string]interface{}{"action": "poll", "pid": "bg-1"}, "Process poll: bg-1"},
		{map[string]interface{}{"action": "spawn", "command": "make build"}, "Process spawn: make build"},
	}

	for _, tt := range tests {
		result := tool.Summarize(tt.input)
		if result != tt.expect {
			t.Errorf("Summarize(%v) = %q, want %q", tt.input, result, tt.expect)
		}
	}
}

func TestListEmptyProcesses(t *testing.T) {
	// Reset registry for clean test
	processRegistry.Lock()
	processRegistry.procs = make(map[string]*ProcessRecord)
	processRegistry.seq = 0
	processRegistry.Unlock()

	tool := ProcessTool{}
	result, err := tool.Execute(map[string]interface{}{"action": "list"}, core.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "No background processes." {
		t.Errorf("got %v, want 'No background processes.'", result.Content)
	}
}
