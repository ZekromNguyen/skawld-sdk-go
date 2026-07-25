package skawld

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
)

type stopProvider struct {
	stop core.StopReason
}

func (p stopProvider) ID() string                     { return "stop" }
func (p stopProvider) ContextWindow(core.ModelID) int { return 10000 }
func (p stopProvider) Stream(ctx context.Context, request core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult, 3)
	out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "message_start", Model: request.Model}}
	out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "text_delta", Text: "partial"}}
	out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "message_end", StopReason: p.stop}}
	close(out)
	return out
}

func TestNonSuccessStopReasonsAreNotReportedAsSuccess(t *testing.T) {
	for _, stop := range []core.StopReason{core.StopMaxTokens, core.StopRefusal, core.StopError} {
		t.Run(string(stop), func(t *testing.T) {
			agent, err := NewAgent(AgentOptions{Provider: stopProvider{stop: stop}, Model: "test"})
			if err != nil {
				t.Fatal(err)
			}
			defer agent.Close()
			session, err := agent.Session(context.Background(), SessionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var result core.Event
			for event := range session.Run(context.Background(), "test", RunOptions{}) {
				if event.Type == core.EventResult {
					result = event
				}
			}
			if result.Subtype == "success" {
				t.Fatalf("stop reason %s was reported as success", stop)
			}
		})
	}
}

type countingErrorProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *countingErrorProvider) ID() string                     { return "counting-error" }
func (p *countingErrorProvider) ContextWindow(core.ModelID) int { return 10000 }
func (p *countingErrorProvider) Stream(context.Context, core.ProviderRequest) core.ProviderStream {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	out := make(chan core.ProviderStreamResult, 1)
	out <- core.ProviderStreamResult{Err: core.NewProviderError("retryable", 503, true, nil)}
	close(out)
	return out
}

func TestProviderRetryPolicyCanExplicitlyDisableRetries(t *testing.T) {
	provider := &countingErrorProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider: provider, Model: "test",
		ProviderRetry: &ProviderRetryPolicy{MaxRetries: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(context.Background(), "test", RunOptions{}) {
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.calls != 1 {
		t.Fatalf("expected exactly one provider attempt, got %d", provider.calls)
	}
}

func TestSessionTenantIsolation(t *testing.T) {
	store := sessions.NewInMemoryStore()
	first, err := NewAgent(AgentOptions{
		Provider: stopProvider{stop: core.StopEndTurn}, Model: "test", SessionStore: store,
		Principal: core.Principal{TenantID: "tenant-a", ActorID: "actor-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Session(context.Background(), SessionOptions{ID: "shared-id"}); err != nil {
		t.Fatal(err)
	}
	second, err := NewAgent(AgentOptions{
		Provider: stopProvider{stop: core.StopEndTurn}, Model: "test", SessionStore: store,
		Principal: core.Principal{TenantID: "tenant-b", ActorID: "actor-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Session(context.Background(), SessionOptions{ID: "shared-id"}); err == nil {
		t.Fatal("expected cross-tenant session access to be rejected")
	}
}

func TestAgentCloseCancelsAndWaitsForActiveRun(t *testing.T) {
	provider := newHandleBlockingProvider()
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	handle := session.StartRun(context.Background(), "wait", RunOptions{})
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- agent.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent.Close did not wait for and stop active run")
	}
	<-handle.Done()
	if _, err := agent.Session(context.Background(), SessionOptions{}); err == nil {
		t.Fatal("closed agent accepted a new session")
	}
}
