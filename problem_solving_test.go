package skawld

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

type problemRecordingProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
}

func (p *problemRecordingProvider) ID() string { return "problem-recording" }
func (p *problemRecordingProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *problemRecordingProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	call := len(p.requests)
	p.mu.Unlock()
	out := make(chan core.ProviderStreamResult, 8)
	go func() {
		defer close(out)
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "message_start", Model: req.Model}}
		if call == 1 {
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "tool_use_start", ID: "read_1", Name: "Read"}}
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "read_1", JSONDelta: `{"file_path":"go.mod"}`}}
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "tool_use_end", ID: "read_1"}}
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}}
			return
		}
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "text_delta", Text: "done"}}
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}}
	}()
	return out
}

func TestProblemSolvingStateInjectedAfterToolUse(t *testing.T) {
	provider := &problemRecordingProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider: provider,
		Model:    "fake",
		Tools:    tools.DefaultTools(),
		Permissions: PermissionOptions{
			Mode: PermissionModeYolo,
		},
		CWD: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(context.Background(), "read go.mod", RunOptions{}) {
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) < 2 {
		t.Fatalf("expected at least two provider turns, got %d", len(provider.requests))
	}
	var stateText string
	for _, block := range provider.requests[1].System {
		if strings.Contains(block.Text, problemSolvingSystemHeader) {
			stateText = block.Text
			break
		}
	}
	if !strings.Contains(stateText, "Inspected files: go.mod") {
		t.Fatalf("expected inspected file in problem state, got:\n%s", stateText)
	}
}
