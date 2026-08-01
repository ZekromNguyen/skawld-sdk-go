package skawld

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

// toolCallEveryTurnProvider proposes one tool call on every turn and never
// ends the turn, so a run can be driven past the cumulative tool-call limit.
type toolCallEveryTurnProvider struct {
	turns int
}

func (*toolCallEveryTurnProvider) ID() string { return "tool-call-limit" }
func (*toolCallEveryTurnProvider) ContextWindow(core.ModelID) int {
	return 10000
}
func (p *toolCallEveryTurnProvider) Stream(
	ctx context.Context,
	req core.ProviderRequest,
) core.ProviderStream {
	out := make(chan core.ProviderStreamResult, 8)
	p.turns++
	turn := p.turns
	go func() {
		defer close(out)
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "message_start", Model: req.Model,
		}}
		id := fmt.Sprintf("call-%d", turn)
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "tool_use_start", ID: id, Name: "customer.lookup",
		}}
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "tool_use_input_delta", ID: id, JSONDelta: `{}`,
		}}
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "tool_use_end", ID: id,
		}}
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "message_end", StopReason: core.StopToolUse,
			Usage: core.Usage{InputTokens: 1, OutputTokens: 1},
		}}
	}()
	return out
}

func newProductionAgentForTest(
	t *testing.T,
	provider core.Provider,
) *Agent {
	t.Helper()
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: provider, Model: "test", Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

// The cumulative tool-call limit must be enforced before the assistant
// message is persisted, so a run that exceeds the limit never leaves a
// dangling tool_use block in the durable history.
func TestProductionToolCallLimitNotPersistedBeforeCheck(t *testing.T) {
	agent := newProductionAgentForTest(t, &toolCallEveryTurnProvider{})
	defer agent.Close()
	ctx := core.WithPrincipal(
		context.Background(),
		core.Principal{TenantID: "tenant-a", ActorID: "actor-a"},
	)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawLimitError := false
	for event := range session.Run(ctx, "lookup", RunOptions{}) {
		if event.Type == core.EventError && event.Error != nil &&
			event.Error.Name == string(core.ErrorValidation) &&
			strings.Contains(event.Error.Message, "tool-call limit") {
			sawLimitError = true
		}
	}
	if !sawLimitError {
		t.Fatal("run did not stop at the cumulative tool-call limit")
	}
	store := agent.store
	stored, err := store.LoadMessages(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistantCount := 0
	pendingToolUses := map[string]bool{}
	for _, storedMsg := range stored {
		msg := storedMsg.Message
		if msg.Role == "assistant" {
			assistantCount++
			for _, block := range msg.Content {
				if block.Type == core.BlockToolUse {
					pendingToolUses[block.ID] = true
				}
			}
		}
		if msg.Role == "user" {
			for _, block := range msg.Content {
				if block.Type == core.BlockToolResult {
					delete(pendingToolUses, block.ToolUseID)
				}
			}
		}
	}
	if assistantCount != 4 {
		t.Fatalf(
			"persisted %d assistant turns, want 4: the limit-exceeding turn must not be stored",
			assistantCount,
		)
	}
	if len(pendingToolUses) != 0 {
		t.Fatalf(
			"dangling tool_use blocks persisted without tool_result: %v",
			pendingToolUses,
		)
	}
}

// The main turn must enforce the same stream lifecycle as the compaction
// guard: text before message_start is a protocol violation.
func TestProductionStreamRejectsTextBeforeMessageStart(t *testing.T) {
	agent := newProductionAgentForTest(t, &productionProtocolTestProvider{
		events: []core.ProviderStreamEvent{
			{Type: "text_delta", Text: "stray"},
			{Type: "message_end", StopReason: core.StopEndTurn},
		},
	})
	defer agent.Close()
	ctx := core.WithPrincipal(
		context.Background(),
		core.Principal{TenantID: "tenant-a", ActorID: "actor-a"},
	)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawProviderError := false
	sawAssistant := false
	for event := range session.Run(ctx, "lookup", RunOptions{}) {
		if event.Type == core.EventError && event.Error != nil &&
			event.Error.Name == string(core.ErrorProvider) {
			sawProviderError = true
		}
		if event.Type == core.EventAssistant {
			sawAssistant = true
		}
	}
	if !sawProviderError || sawAssistant {
		t.Fatalf(
			"text_delta before message_start was not rejected: providerError=%v assistant=%v",
			sawProviderError, sawAssistant,
		)
	}
}

// Concurrent appends must never jointly exceed MaxSessionBytes: the budget
// check and the history update happen under one lock.
func TestProductionSessionAppendHoldsByteBudgetAtomically(t *testing.T) {
	agent := newProductionAgentForTest(t, &productionTestProvider{})
	defer agent.Close()
	ctx := core.WithPrincipal(
		context.Background(),
		core.Principal{TenantID: "tenant-a", ActorID: "actor-a"},
	)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	msg := core.Message{
		Role:    "user",
		Content: []core.ContentBlock{core.Text(strings.Repeat("x", 512))},
	}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = session.append(ctx, []core.Message{msg})
		}()
	}
	wg.Wait()
	session.providerMu.Lock()
	total := estimateMessagesProviderChars(session.completeHistory)
	session.providerMu.Unlock()
	limit := productionLimitsForTest().MaxSessionBytes
	if total > limit {
		t.Fatalf(
			"session history exceeded MaxSessionBytes: %d > %d",
			total, limit,
		)
	}
}
