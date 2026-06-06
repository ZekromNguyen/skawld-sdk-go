package skawld

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/permissions"
	"github.com/skawld/skawld-sdk-go/sessions"
	"github.com/skawld/skawld-sdk-go/tools"
)

type skillProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
}

func (p *skillProvider) ID() string { return "skill-provider" }
func (p *skillProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *skillProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
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
			out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: "skill_1", Name: "Skill"}
			out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "skill_1", JSONDelta: `{"skill":"review","arguments":"main.go"}`}
			out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: "skill_1"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
		case 2:
			out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: "write_1", Name: "WriteProbe"}
			out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "write_1", JSONDelta: `{}`}
			out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: "write_1"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
		default:
			out <- core.ProviderStreamEvent{Type: "text_delta", Text: "done"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
		}
	}()
	return out, errs
}

type writeProbeTool struct {
	calls *int
}

func (t writeProbeTool) Name() string { return "WriteProbe" }
func (t writeProbeTool) Description() string {
	return "write probe"
}
func (t writeProbeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t writeProbeTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (t writeProbeTool) ParallelSafe() bool    { return false }
func (t writeProbeTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (t writeProbeTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	*t.calls = *t.calls + 1
	return core.ToolResult{Content: "wrote", Summary: "wrote"}, nil
}
func (t writeProbeTool) Summarize(input map[string]interface{}) string { return "write probe" }

func TestSkillInvocationAppliesOneTurnOverlayAndPersists(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "review", `---
description: Review skill
allowed_tools: [WriteProbe]
model: skill-model
---
Review $ARGUMENTS now.`)
	store := sessions.NewInMemoryStore()
	provider := &skillProvider{}
	reg := tools.NewRegistry()
	calls := 0
	if err := reg.Register(writeProbeTool{calls: &calls}); err != nil {
		t.Fatal(err)
	}
	var permissionCallbacks int
	agent, err := NewAgent(AgentOptions{
		Provider:     provider,
		Model:        "base-model",
		Tools:        reg,
		SessionStore: store,
		SkillsDir:    dir,
		Permissions: PermissionOptions{
			Mode: core.PermissionModeDefault,
			CanUseTool: func(ctx context.Context, req permissions.CanUseToolRequest) (permissions.CanUseToolResponse, error) {
				permissionCallbacks++
				return permissions.CanUseToolResponse{Behavior: "deny", Message: "no"}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{ID: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	var sawLoaded, sawInvoked, sawCompleted bool
	for ev := range session.Run(context.Background(), "use skill", RunOptions{}) {
		switch ev.Type {
		case EventSkillsLoaded:
			sawLoaded = true
		case EventSkillInvoked:
			sawInvoked = true
		case EventSkillCompleted:
			sawCompleted = true
		}
	}
	if !sawLoaded || !sawInvoked || !sawCompleted {
		t.Fatalf("expected skill events loaded=%t invoked=%t completed=%t", sawLoaded, sawInvoked, sawCompleted)
	}
	if calls != 1 {
		t.Fatalf("expected write probe to run via allowed-tools overlay, got %d", calls)
	}
	if permissionCallbacks != 0 {
		t.Fatalf("expected overlay to avoid permission prompt callback, got %d callbacks", permissionCallbacks)
	}
	loaded, ok, err := store.Load("skills")
	if err != nil || !ok {
		t.Fatalf("load failed: ok=%t err=%v", ok, err)
	}
	if len(loaded.InvokedSkills) != 1 || !strings.Contains(loaded.InvokedSkills[0].SubstitutedBody, "Review main.go now.") {
		t.Fatalf("unexpected invoked skill persistence: %+v", loaded.InvokedSkills)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.requests[1].Model != "skill-model" {
		t.Fatalf("expected skill model override, got %s", provider.requests[1].Model)
	}
	if !containsSystem(provider.requests[1].System, "Review main.go now.") {
		t.Fatalf("expected skill body in second request system: %+v", provider.requests[1].System)
	}
	if provider.requests[2].Model != "base-model" || containsSystem(provider.requests[2].System, "Review main.go now.") {
		t.Fatalf("expected overlay to clear after one assistant turn; request=%+v", provider.requests[2])
	}
}

func TestSkillResumeAndCompactionReplay(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "review", `---
description: Review skill
---
Review body`)
	store := sessions.NewInMemoryStore()
	if _, err := store.Create("resume", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInvokedSkills("resume", []core.InvokedSkillRecord{{Name: "review", SubstitutedBody: "Review body", InvokedAt: 1}}); err != nil {
		t.Fatal(err)
	}
	provider := &recordingCompactionProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider:            provider,
		Model:               "fake-model",
		SessionStore:        store,
		SkillsDir:           dir,
		CompactionThreshold: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{ID: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := session.append([]core.Message{
			{Role: "user", Content: []core.ContentBlock{core.Text(strings.Repeat("old user ", 12))}},
			{Role: "assistant", Content: []core.ContentBlock{core.Text(strings.Repeat("old assistant ", 12))}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for range session.Run(context.Background(), "continue", RunOptions{}) {
	}
	provider.mu.Lock()
	last := provider.requests[len(provider.requests)-1]
	provider.mu.Unlock()
	var found bool
	for _, msg := range last.Messages {
		if strings.Contains(firstMessageText(msg), "Review body") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected invoked skill body in compacted provider view: %+v", last.Messages)
	}
}

func containsSystem(blocks []core.SystemBlock, text string) bool {
	for _, block := range blocks {
		if strings.Contains(block.Text, text) {
			return true
		}
	}
	return false
}

func firstMessageText(msg core.Message) string {
	for _, block := range msg.Content {
		if block.Type == core.BlockText {
			return block.Text
		}
	}
	return ""
}

func writeSkillFile(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
