package skawld

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/tools"
)

type subagentProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
}

func (p *subagentProvider) ID() string { return "subagent-provider" }
func (p *subagentProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *subagentProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.mu.Lock()
	p.requests = append(p.requests, req)
	call := len(p.requests)
	p.mu.Unlock()
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		switch call {
		case 1:
			out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: "sub_1", Name: "Subagent"}
			out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "sub_1", JSONDelta: `{"agent":"review","task":"inspect target"}`}
			out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: "sub_1"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
		case 2:
			out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: "probe_1", Name: "Probe"}
			out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "probe_1", JSONDelta: `{}`}
			out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: "probe_1"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
		case 3:
			out <- core.ProviderStreamEvent{Type: "text_delta", Text: "child done"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
		default:
			out <- core.ProviderStreamEvent{Type: "text_delta", Text: "parent done"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
		}
	}()
	return out, errs
}

type probeTool struct {
	calls *int
}

func (t probeTool) Name() string        { return "Probe" }
func (t probeTool) Description() string { return "probe" }
func (t probeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t probeTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (t probeTool) ParallelSafe() bool    { return false }
func (t probeTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (t probeTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	*t.calls++
	return core.ToolResult{Content: "probe ok", Summary: "probe"}, nil
}
func (t probeTool) Summarize(input map[string]interface{}) string { return "probe" }

func TestSubagentToolRunsChildSessionAndWrapsEvents(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "review.md", `---
name: review
description: Review helper
tools: [Probe]
model: child-model
---
Review only.
`)
	calls := 0
	reg := tools.NewRegistry()
	if err := reg.Register(probeTool{calls: &calls}); err != nil {
		t.Fatal(err)
	}
	provider := &subagentProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider:  provider,
		Model:     "parent-model",
		Tools:     reg,
		AgentsDir: dir,
		SkillsDir: filepath.Join(t.TempDir(), "missing-skills"),
		Permissions: PermissionOptions{
			Mode: PermissionModeYolo,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sawSubagentResult, sawParentResult bool
	for ev := range session.Run(context.Background(), "delegate", RunOptions{}) {
		if ev.Type == EventSubagent && ev.Subtype == "review" {
			child, _ := ev.Delta["event"].(core.Event)
			if child.Type == EventResult && child.FinalText == "child done" {
				sawSubagentResult = true
			}
		}
		if ev.Type == EventResult && ev.FinalText == "parent done" {
			sawParentResult = true
		}
	}
	if !sawSubagentResult {
		t.Fatal("expected wrapped child result event")
	}
	if !sawParentResult {
		t.Fatal("expected parent final result")
	}
	if calls != 1 {
		t.Fatalf("expected filtered child Probe tool call, got %d", calls)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) < 4 {
		t.Fatalf("expected parent/child requests, got %d", len(provider.requests))
	}
	if provider.requests[1].Model != "child-model" {
		t.Fatalf("expected child model override, got %s", provider.requests[1].Model)
	}
	if !containsSystem(provider.requests[1].System, "Review only.") {
		t.Fatalf("expected child system prompt: %+v", provider.requests[1].System)
	}
	if hasTool(provider.requests[1].Tools, "Subagent") || hasTool(provider.requests[1].Tools, "Skill") {
		t.Fatalf("unexpected child tools: %+v", provider.requests[1].Tools)
	}
	if !hasTool(provider.requests[1].Tools, "Probe") {
		t.Fatalf("expected Probe tool in child request: %+v", provider.requests[1].Tools)
	}
}

func TestSubagentListingIncludesDefaultInSystemPrompt(t *testing.T) {
	provider := &singleTextProvider{text: "done"}
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(context.Background(), "hello", RunOptions{}) {
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) == 0 {
		t.Fatal("expected provider request")
	}
	var found bool
	for _, block := range provider.requests[0].System {
		if strings.Contains(block.Text, "Available subagents") && strings.Contains(block.Text, "default") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected subagent listing in system prompt: %+v", provider.requests[0].System)
	}
}

func TestNestedSubagentEventsAreWrapped(t *testing.T) {
	outer := core.Event{Type: EventSubagent, Subtype: "inner", Delta: map[string]interface{}{"agent": "inner"}}
	wrapped := wrapSubagentEvent("outer", outer)
	if wrapped.Type != EventSubagent || wrapped.Subtype != "outer" {
		t.Fatalf("unexpected wrapped event: %+v", wrapped)
	}
	child, ok := wrapped.Delta["event"].(core.Event)
	if !ok {
		t.Fatalf("expected nested event payload: %+v", wrapped.Delta)
	}
	if child.Type != EventSubagent || child.Subtype != "inner" {
		t.Fatalf("unexpected nested event: %+v", child)
	}
}

type subagentCancelProvider struct {
	mu         sync.Mutex
	calls      int
	childStart chan struct{}
	childDone  chan struct{}
}

func newSubagentCancelProvider() *subagentCancelProvider {
	return &subagentCancelProvider{childStart: make(chan struct{}), childDone: make(chan struct{})}
}

func (p *subagentCancelProvider) ID() string { return "subagent-cancel" }
func (p *subagentCancelProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *subagentCancelProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult)
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
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
		if call == 1 {
			if !send(core.ProviderStreamEvent{Type: "tool_use_start", ID: "sub_1", Name: "Subagent"}) {
				return
			}
			if !send(core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "sub_1", JSONDelta: `{"agent":"default","task":"wait"}`}) {
				return
			}
			if !send(core.ProviderStreamEvent{Type: "tool_use_end", ID: "sub_1"}) {
				return
			}
			_ = send(core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse})
			return
		}
		close(p.childStart)
		if !send(core.ProviderStreamEvent{Type: "text_delta", Text: "child partial"}) {
			return
		}
		<-ctx.Done()
		close(p.childDone)
	}()
	return out
}

func TestSubagentCancellationStopsChildEvents(t *testing.T) {
	provider := newSubagentCancelProvider()
	agent, err := NewAgent(AgentOptions{
		Provider:               provider,
		Model:                  "fake-model",
		IncludePartialMessages: true,
		Permissions: PermissionOptions{
			Mode: PermissionModeYolo,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	handle := session.StartRun(context.Background(), "delegate", RunOptions{})
	var sawChildStart bool
	for ev := range handle.Events() {
		if ev.Type != EventSubagent {
			continue
		}
		child, ok := ev.Delta["event"].(core.Event)
		if ok && child.Type == EventPartialAssistant {
			sawChildStart = true
			handle.Close()
			break
		}
	}
	if !sawChildStart {
		t.Fatal("expected child subagent event")
	}
	select {
	case <-provider.childDone:
	case <-time.After(time.Second):
		t.Fatal("child provider stream was not canceled")
	}
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("parent run did not finish after close")
	}
}

type singleTextProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
	text     string
}

func (p *singleTextProvider) ID() string { return "single-text" }
func (p *singleTextProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *singleTextProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: p.text}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
	}()
	return out, errs
}

func writeAgentFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasTool(schemas []core.ToolSchema, name string) bool {
	for _, schema := range schemas {
		if schema.Name == name {
			return true
		}
	}
	return false
}
