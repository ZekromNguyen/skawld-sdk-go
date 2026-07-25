package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

type Scenario struct {
	Name         string
	Prompt       string
	Files        map[string]string
	Provider     []Turn
	AllowedTools []string
	MaxTurns     int
	Checks       []Check
}

type Turn struct {
	Text      string
	ToolCalls []ToolCall
}

type ToolCall struct {
	ID    string
	Name  string
	Input string
}

type Result struct {
	Dir    string
	Events []core.Event
}

type Check func(*testing.T, Result)

func Run(t *testing.T, scenario Scenario) Result {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range scenario.Files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	registry := tools.DefaultTools()
	if len(scenario.AllowedTools) > 0 {
		allowed := map[string]bool{}
		for _, name := range scenario.AllowedTools {
			allowed[name] = true
		}
		for _, name := range registry.Names() {
			if !allowed[name] {
				registry.Unregister(name)
			}
		}
	}
	maxTurns := scenario.MaxTurns
	if maxTurns == 0 {
		maxTurns = 20
	}
	agent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider: &scriptedProvider{turns: scenario.Provider},
		Model:    "scripted",
		Tools:    registry,
		Permissions: skawld.PermissionOptions{
			Mode: skawld.PermissionModeYolo,
		},
		CWD:      dir,
		MaxTurns: maxTurns,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	session, err := agent.Session(context.Background(), skawld.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var events []core.Event
	for ev := range session.Run(context.Background(), scenario.Prompt, skawld.RunOptions{}) {
		events = append(events, ev)
	}
	result := Result{Dir: dir, Events: events}
	for _, check := range scenario.Checks {
		check(t, result)
	}
	return result
}

type scriptedProvider struct {
	mu    sync.Mutex
	turns []Turn
	calls int
}

func (p *scriptedProvider) ID() string { return "harness-scripted" }
func (p *scriptedProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *scriptedProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	p.mu.Lock()
	call := p.calls
	p.calls++
	var turn Turn
	if call < len(p.turns) {
		turn = p.turns[call]
	} else {
		turn = Turn{Text: "done"}
	}
	p.mu.Unlock()
	out := make(chan core.ProviderStreamResult, 16)
	go func() {
		defer close(out)
		send := func(ev core.ProviderStreamEvent) bool {
			select {
			case out <- core.ProviderStreamResult{Event: ev}:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !send(core.ProviderStreamEvent{Type: "message_start", Model: req.Model}) {
			return
		}
		for _, call := range turn.ToolCalls {
			id := call.ID
			if id == "" {
				id = "call_" + call.Name
			}
			if !send(core.ProviderStreamEvent{Type: "tool_use_start", ID: id, Name: call.Name}) {
				return
			}
			if call.Input != "" {
				if !send(core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: id, JSONDelta: call.Input}) {
					return
				}
			}
			if !send(core.ProviderStreamEvent{Type: "tool_use_end", ID: id}) {
				return
			}
		}
		if turn.Text != "" {
			if !send(core.ProviderStreamEvent{Type: "text_delta", Text: turn.Text}) {
				return
			}
		}
		stop := core.StopEndTurn
		if len(turn.ToolCalls) > 0 {
			stop = core.StopToolUse
		}
		_ = send(core.ProviderStreamEvent{Type: "message_end", StopReason: stop})
	}()
	return out
}

func FileContains(rel, want string) Check {
	return func(t *testing.T, result Result) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(result.Dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), want) {
			t.Fatalf("expected %s to contain %q, got:\n%s", rel, want, string(raw))
		}
	}
}

func ToolCalled(name string) Check {
	return func(t *testing.T, result Result) {
		t.Helper()
		for _, ev := range result.Events {
			if ev.Type == core.EventToolCallStart && ev.ToolName == name {
				return
			}
		}
		t.Fatalf("expected tool %s to be called; events: %s", name, eventSummary(result.Events))
	}
}

func ToolOrder(first, second string) Check {
	return func(t *testing.T, result Result) {
		t.Helper()
		firstIndex, secondIndex := -1, -1
		for i, ev := range result.Events {
			if ev.Type != core.EventToolCallStart {
				continue
			}
			if ev.ToolName == first && firstIndex == -1 {
				firstIndex = i
			}
			if ev.ToolName == second && secondIndex == -1 {
				secondIndex = i
			}
		}
		if firstIndex == -1 || secondIndex == -1 || firstIndex >= secondIndex {
			t.Fatalf("expected %s before %s; events: %s", first, second, eventSummary(result.Events))
		}
	}
}

func SuccessfulResult() Check {
	return func(t *testing.T, result Result) {
		t.Helper()
		for _, ev := range result.Events {
			if ev.Type == core.EventResult && ev.Subtype == "success" {
				return
			}
		}
		t.Fatalf("expected successful result; events: %s", eventSummary(result.Events))
	}
}

func eventSummary(events []core.Event) string {
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		if ev.ToolName != "" {
			parts = append(parts, fmt.Sprintf("%s:%s", ev.Type, ev.ToolName))
		} else {
			parts = append(parts, string(ev.Type))
		}
	}
	return strings.Join(parts, ", ")
}
