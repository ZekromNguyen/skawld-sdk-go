package skawld

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
)

type recordingCompactionProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
}

func (p *recordingCompactionProvider) ID() string { return "compaction" }
func (p *recordingCompactionProvider) ContextWindow(model core.ModelID) int {
	return 160
}
func (p *recordingCompactionProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
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
		if len(req.Messages) == 1 && req.Messages[0].Role == "user" && strings.Contains(req.Messages[0].Content[0].Text, "Earlier conversation:") {
			out <- core.ProviderStreamEvent{Type: "text_delta", Text: "summary of earlier turns"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "answer"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn, Usage: core.Usage{InputTokens: call, OutputTokens: 1}}
	}()
	return out, errs
}

func TestDefaultCompactionChangesProviderViewOnly(t *testing.T) {
	store := sessions.NewInMemoryStore()
	provider := &recordingCompactionProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider:            provider,
		Model:               "fake-model",
		SessionStore:        store,
		CompactionThreshold: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{ID: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if err := session.append(ctx, []core.Message{
			{Role: "user", Content: []core.ContentBlock{core.Text(strings.Repeat("old user ", 12))}},
			{Role: "assistant", Content: []core.ContentBlock{core.Text(strings.Repeat("old assistant ", 12))}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	var compactions []core.Event
	for ev := range session.Run(context.Background(), "continue", RunOptions{}) {
		if ev.Type == EventCompaction {
			compactions = append(compactions, ev)
		}
	}
	if len(compactions) != 1 {
		t.Fatalf("expected one compaction event, got %d", len(compactions))
	}
	if compactions[0].Subtype != compactionTriggerProactive {
		t.Fatalf("expected proactive compaction, got %q", compactions[0].Subtype)
	}
	if compactions[0].MessagesAfter >= compactions[0].MessagesBefore {
		t.Fatalf("expected provider view to shrink, before=%d after=%d", compactions[0].MessagesBefore, compactions[0].MessagesAfter)
	}
	stored, err := store.LoadMessages(ctx, "compact")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 26 {
		t.Fatalf("expected full stored history to keep all messages, got %d", len(stored))
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) < 2 {
		t.Fatalf("expected summary and answer requests, got %d", len(provider.requests))
	}
	last := provider.requests[len(provider.requests)-1]
	if len(last.Messages) >= len(stored) {
		t.Fatalf("expected compacted provider request, got %d messages for %d stored", len(last.Messages), len(stored))
	}
	if !isCompactionSummary(last.Messages[0]) {
		t.Fatal("expected compacted provider request to start with a summary")
	}
}

func TestCompactionReinjectsInvokedSkillsProviderOnly(t *testing.T) {
	store := sessions.NewInMemoryStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, "skills", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInvokedSkills(ctx, "skills", []core.InvokedSkillRecord{
		{Name: "review", SubstitutedBody: "Review carefully.", InvokedAt: 10},
		{Name: "test", SubstitutedBody: "Run tests.", InvokedAt: 20},
	}); err != nil {
		t.Fatal(err)
	}
	provider := &recordingCompactionProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider:            provider,
		Model:               "fake-model",
		SessionStore:        store,
		CompactionThreshold: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{ID: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := session.append(ctx, []core.Message{
			{Role: "user", Content: []core.ContentBlock{core.Text(strings.Repeat("old user ", 12))}},
			{Role: "assistant", Content: []core.ContentBlock{core.Text(strings.Repeat("old assistant ", 12))}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for ev := range session.Run(context.Background(), "continue", RunOptions{}) {
		if ev.Type == EventResult {
			break
		}
	}
	provider.mu.Lock()
	last := provider.requests[len(provider.requests)-1]
	provider.mu.Unlock()
	var replayCount int
	var replayText string
	for _, msg := range last.Messages {
		if isSkillReplayMessage(msg) {
			replayCount++
			replayText = msg.Content[0].Text
		}
	}
	if replayCount != 1 {
		t.Fatalf("expected one skill replay message, got %d in %s", replayCount, prettyMessages(last.Messages))
	}
	if !strings.Contains(replayText, "review") || !strings.Contains(replayText, "Review carefully.") || !strings.Contains(replayText, "test") {
		t.Fatalf("unexpected skill replay text: %s", replayText)
	}
	stored, err := store.LoadMessages(ctx, "skills")
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range stored {
		if isSkillReplayMessage(msg.Message) {
			t.Fatal("skill replay message should not be persisted in stored history")
		}
	}
}

func TestCompactionSkillReplayDoesNotDuplicateAcrossRuns(t *testing.T) {
	store := sessions.NewInMemoryStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, "skills-dedup", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInvokedSkills(ctx, "skills-dedup", []core.InvokedSkillRecord{
		{Name: "review", SubstitutedBody: "Review carefully."},
	}); err != nil {
		t.Fatal(err)
	}
	provider := &recordingCompactionProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider:            provider,
		Model:               "fake-model",
		SessionStore:        store,
		CompactionThreshold: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{ID: "skills-dedup"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := session.append(ctx, []core.Message{
			{Role: "user", Content: []core.ContentBlock{core.Text(strings.Repeat("old user ", 12))}},
			{Role: "assistant", Content: []core.ContentBlock{core.Text(strings.Repeat("old assistant ", 12))}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for range session.Run(context.Background(), "first", RunOptions{}) {
	}
	for range session.Run(context.Background(), strings.Repeat("second ", 40), RunOptions{}) {
	}
	provider.mu.Lock()
	last := provider.requests[len(provider.requests)-1]
	provider.mu.Unlock()
	var replayCount int
	for _, msg := range last.Messages {
		if isSkillReplayMessage(msg) {
			replayCount++
		}
	}
	if replayCount != 1 {
		t.Fatalf("expected one replay after repeated compactions, got %d in %s", replayCount, prettyMessages(last.Messages))
	}
}

type noOpCountingStrategy struct {
	calls int
}

func (s *noOpCountingStrategy) Name() string { return "no-op-counting" }
func (s *noOpCountingStrategy) Compact(ctx context.Context, req CompactionRequest) (CompactionResult, error) {
	s.calls++
	return CompactionResult{}, nil
}

func TestCompactionNoOpSuppressesEvent(t *testing.T) {
	strategy := &noOpCountingStrategy{}
	agent, err := NewAgent(AgentOptions{
		Provider:            &recordingCompactionProvider{},
		Model:               "fake-model",
		CompactionStrategy:  strategy,
		CompactionThreshold: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sawCompaction bool
	for ev := range session.Run(context.Background(), strings.Repeat("large prompt ", 40), RunOptions{}) {
		if ev.Type == EventCompaction {
			sawCompaction = true
		}
	}
	if strategy.calls == 0 {
		t.Fatal("expected strategy to be called")
	}
	if sawCompaction {
		t.Fatal("did not expect compaction event for no-op result")
	}
}

func TestKeepLastTurnsDoesNotRepeatedlySummarizeOnlyPriorSummary(t *testing.T) {
	provider := &recordingCompactionProvider{}
	strategy := KeepLastTurnsCompactionStrategy{Turns: 2}
	req := CompactionRequest{
		Provider: provider,
		Model:    "fake-model",
		Messages: []core.Message{
			compactionSummaryMessage("prior summary"),
			{Role: "user", Content: []core.ContentBlock{core.Text("kept one")}},
			{Role: "assistant", Content: []core.ContentBlock{core.Text("response one")}},
			{Role: "user", Content: []core.ContentBlock{core.Text("kept two")}},
			{Role: "assistant", Content: []core.ContentBlock{core.Text("response two")}},
		},
	}
	result, err := strategy.Compact(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("expected no-op when only the existing summary would be dropped")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 0 {
		t.Fatalf("expected no summary provider call, got %d", len(provider.requests))
	}
}

func prettyMessages(messages []core.Message) string {
	raw, _ := json.Marshal(messages)
	return string(raw)
}

type contextLengthProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
}

func (p *contextLengthProvider) ID() string { return "context-length" }
func (p *contextLengthProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *contextLengthProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
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
		if len(req.Messages) == 1 && strings.Contains(req.Messages[0].Content[0].Text, "Earlier conversation:") {
			out <- core.ProviderStreamEvent{Type: "text_delta", Text: "forced summary"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
			return
		}
		if call == 1 {
			errs <- &core.SkawldError{Kind: core.ErrorContextLength, Message: "too many tokens", Cause: errors.New("limit")}
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "after compaction"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
	}()
	return out, errs
}

func TestContextLengthErrorForcesOneCompactionRetry(t *testing.T) {
	provider := &contextLengthProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider:           provider,
		Model:              "fake-model",
		CompactionStrategy: KeepLastTurnsCompactionStrategy{Turns: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := session.append(ctx, []core.Message{
		{Role: "user", Content: []core.ContentBlock{core.Text("old")}},
		{Role: "assistant", Content: []core.ContentBlock{core.Text("old response")}},
	}); err != nil {
		t.Fatal(err)
	}
	var sawForced bool
	var final string
	for ev := range session.Run(context.Background(), "new", RunOptions{}) {
		if ev.Type == EventCompaction && ev.Subtype == compactionTriggerForced {
			sawForced = true
		}
		if ev.Type == EventResult {
			final = ev.FinalText
		}
	}
	if !sawForced {
		t.Fatal("expected forced compaction event")
	}
	if final != "after compaction" {
		t.Fatalf("expected successful retry after compaction, got %q", final)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 3 {
		t.Fatalf("expected initial, summary, and retry provider requests, got %d", len(provider.requests))
	}
	if !isCompactionSummary(provider.requests[2].Messages[0]) {
		t.Fatal("expected retry request to use compacted provider view")
	}
}
