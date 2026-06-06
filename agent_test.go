package skawld

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/permissions"
	"github.com/skawld/skawld-sdk-go/tools"
)

type fakeProvider struct {
	calls int
}

func (p *fakeProvider) ID() string { return "fake" }
func (p *fakeProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *fakeProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.calls++
	call := p.calls
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		if call == 1 {
			out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: "toolu_1", Name: "Read"}
			out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "toolu_1", JSONDelta: `{"file_path":"go.mod"}`}
			out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: "toolu_1"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}}
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "done"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn, Usage: core.Usage{InputTokens: 12, OutputTokens: 3}}
	}()
	return out, errs
}

func TestAgentRunExecutesToolAndReturnsResult(t *testing.T) {
	fp := &fakeProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider: fp,
		Model:    "fake-model",
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
	var sawToolStart, sawResult bool
	for ev := range session.Run(context.Background(), "read go.mod", RunOptions{}) {
		if ev.Type == EventToolCallStart && ev.ToolName == "Read" {
			sawToolStart = true
		}
		if ev.Type == EventResult && ev.Subtype == "success" && ev.FinalText == "done" {
			sawResult = true
		}
	}
	if !sawToolStart {
		t.Fatal("expected Read tool_call_start")
	}
	if !sawResult {
		t.Fatal("expected successful final result")
	}
}

type blockingProvider struct{}

func (p *blockingProvider) ID() string { return "blocking" }
func (p *blockingProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *blockingProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		<-ctx.Done()
		errs <- core.NewAbortError("aborted", ctx.Err())
	}()
	return out, errs
}

func TestSessionAbortActiveRunEmitsAbortedResult(t *testing.T) {
	agent, err := NewAgent(AgentOptions{Provider: &blockingProvider{}, Model: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events := session.Run(context.Background(), "wait", RunOptions{})
	session.Abort()
	session.Abort()
	var sawAborted bool
	for ev := range events {
		if ev.Type == EventResult && ev.Subtype == "aborted" {
			sawAborted = true
		}
	}
	if !sawAborted {
		t.Fatal("expected aborted result")
	}
	session.Abort()
}

type handleBlockingProvider struct {
	started chan struct{}
	ctxDone chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newHandleBlockingProvider() *handleBlockingProvider {
	return &handleBlockingProvider{started: make(chan struct{}), ctxDone: make(chan struct{}), closed: make(chan struct{})}
}

func (p *handleBlockingProvider) ID() string { return "handle-blocking" }
func (p *handleBlockingProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *handleBlockingProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult)
	go func() {
		defer close(out)
		defer close(p.closed)
		close(p.started)
		select {
		case out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "message_start", Model: req.Model}}:
		case <-ctx.Done():
			p.once.Do(func() { close(p.ctxDone) })
			return
		}
		<-ctx.Done()
		p.once.Do(func() { close(p.ctxDone) })
	}()
	return out
}

func TestRunHandleAbortStillEmitsAbortedResult(t *testing.T) {
	provider := newHandleBlockingProvider()
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	handle := session.StartRun(context.Background(), "wait", RunOptions{})
	if ev := <-handle.Events(); ev.Type != EventSystem {
		t.Fatalf("expected system event, got %s", ev.Type)
	}
	handle.Abort()
	var sawAborted bool
	for ev := range handle.Events() {
		if ev.Type == EventResult && ev.Subtype == "aborted" {
			sawAborted = true
		}
	}
	if !sawAborted {
		t.Fatal("expected aborted result after handle abort")
	}
}

func TestRunHandleCloseCleansUpAbandonedRun(t *testing.T) {
	provider := newHandleBlockingProvider()
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	handle := session.StartRun(context.Background(), "wait", RunOptions{})
	if ev := <-handle.Events(); ev.Type != EventSystem {
		t.Fatalf("expected system event, got %s", ev.Type)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider stream did not start")
	}
	handle.Close()
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("run handle did not finish after close")
	}
	select {
	case <-provider.ctxDone:
	case <-time.After(time.Second):
		t.Fatal("provider context was not canceled")
	}
	nextProvider := newHandleBlockingProvider()
	session.agent.opts.Provider = nextProvider
	events := session.Run(context.Background(), "next", RunOptions{})
	session.Abort()
	var sawAborted bool
	for ev := range events {
		if ev.Type == EventResult && ev.Subtype == "aborted" {
			sawAborted = true
		}
	}
	if !sawAborted {
		t.Fatal("expected session to accept a new run after closing abandoned handle")
	}
}

func TestSessionRunContextCancelCleansUpStoppedConsumer(t *testing.T) {
	provider := newHandleBlockingProvider()
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := session.Run(ctx, "wait", RunOptions{})
	if ev := <-events; ev.Type != EventSystem {
		t.Fatalf("expected system event, got %s", ev.Type)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider stream did not start")
	}
	cancel()
	waitFor(t, time.Second, func() bool {
		session.activeMu.Lock()
		defer session.activeMu.Unlock()
		return !session.active
	})
	nextProvider := newHandleBlockingProvider()
	session.agent.opts.Provider = nextProvider
	next := session.StartRun(context.Background(), "next", RunOptions{})
	select {
	case ev := <-next.Events():
		if ev.Type != EventSystem {
			t.Fatalf("expected new run system event, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("new run did not start after canceled abandoned Session.Run")
	}
	next.Close()
}

func TestSessionRejectsConcurrentRun(t *testing.T) {
	agent, err := NewAgent(AgentOptions{Provider: &blockingProvider{}, Model: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first := session.Run(context.Background(), "first", RunOptions{})
	second := session.Run(context.Background(), "second", RunOptions{})
	var sawConfigError bool
	for ev := range second {
		if ev.Type == EventError && ev.Error != nil && ev.Error.Name == "ConfigError" {
			sawConfigError = true
		}
	}
	if !sawConfigError {
		t.Fatal("expected concurrent run config error")
	}
	session.Abort()
	for range first {
	}
}

type retryProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *retryProvider) ID() string { return "retry" }
func (p *retryProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *retryProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		if call == 1 {
			errs <- core.NewProviderError("temporary", 503, true, errors.New("temporary"))
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "retried"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}}
	}()
	return out, errs
}

func TestProviderRetryBeforeCommitSucceeds(t *testing.T) {
	provider := &retryProvider{}
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "fake-model", MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var final string
	for ev := range session.Run(context.Background(), "retry", RunOptions{}) {
		if ev.Type == EventResult {
			final = ev.FinalText
		}
	}
	if final != "retried" {
		t.Fatalf("expected retried final text, got %q", final)
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 provider attempts, got %d", provider.calls)
	}
}

type retryExhaustProvider struct {
	calls int
}

func (p *retryExhaustProvider) ID() string { return "retry-exhaust" }
func (p *retryExhaustProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *retryExhaustProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.calls++
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		errs <- core.NewProviderError("still down", 503, true, errors.New("still down"))
	}()
	return out, errs
}

func TestProviderRetryExhaustionEmitsTypedError(t *testing.T) {
	provider := &retryExhaustProvider{}
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "fake-model", MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sawProviderError bool
	for ev := range session.Run(context.Background(), "retry", RunOptions{}) {
		if ev.Type == EventError && ev.Error != nil && ev.Error.Name == string(core.ErrorProvider) && ev.Error.Retryable {
			sawProviderError = true
		}
	}
	if !sawProviderError {
		t.Fatal("expected retryable provider error event")
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 provider attempts, got %d", provider.calls)
	}
}

type partialErrorStreamProvider struct{}

func (p *partialErrorStreamProvider) ID() string { return "partial-error-stream" }
func (p *partialErrorStreamProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *partialErrorStreamProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult)
	go func() {
		defer close(out)
		select {
		case out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "message_start", Model: req.Model}}:
		case <-ctx.Done():
			return
		}
		select {
		case out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{Type: "text_delta", Text: "partial"}}:
		case <-ctx.Done():
			return
		}
		select {
		case out <- core.ProviderStreamResult{Err: core.NewProviderError("after partial", 503, true, errors.New("after partial"))}:
		case <-ctx.Done():
		}
	}()
	return out
}

func TestProviderErrorAfterPartialOutputEmitsErrorWithoutRetry(t *testing.T) {
	provider := &partialErrorStreamProvider{}
	agent, err := NewAgent(AgentOptions{
		Provider:               provider,
		Model:                  "fake-model",
		MaxRetries:             3,
		IncludePartialMessages: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sawPartial, sawProviderError bool
	for ev := range session.Run(context.Background(), "partial error", RunOptions{}) {
		if ev.Type == EventPartialAssistant && ev.Delta["text"] == "partial" {
			sawPartial = true
		}
		if ev.Type == EventError && ev.Error != nil && ev.Error.Name == string(core.ErrorProvider) {
			sawProviderError = true
		}
	}
	if !sawPartial {
		t.Fatal("expected partial assistant event before provider error")
	}
	if !sawProviderError {
		t.Fatal("expected provider error event")
	}
}

type cancelAfterDeltaProvider struct {
	kind    string
	started chan struct{}
	ctxDone chan struct{}
}

func newCancelAfterDeltaProvider(kind string) *cancelAfterDeltaProvider {
	return &cancelAfterDeltaProvider{kind: kind, started: make(chan struct{}), ctxDone: make(chan struct{})}
}

func (p *cancelAfterDeltaProvider) ID() string { return "cancel-after-delta" }
func (p *cancelAfterDeltaProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *cancelAfterDeltaProvider) Stream(ctx context.Context, req core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult)
	go func() {
		defer close(out)
		defer close(p.ctxDone)
		close(p.started)
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
		switch p.kind {
		case "text":
			if !send(core.ProviderStreamEvent{Type: "text_delta", Text: "partial"}) {
				return
			}
		case "thinking":
			if !send(core.ProviderStreamEvent{Type: "thinking_delta", Text: "partial"}) {
				return
			}
		case "tool_use_input":
			if !send(core.ProviderStreamEvent{Type: "tool_use_start", ID: "call_1", Name: "TaskList"}) {
				return
			}
			if !send(core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "call_1", JSONDelta: `{}`}) {
				return
			}
		}
		<-ctx.Done()
	}()
	return out
}

func TestProviderStreamCancellationAfterPartialDeltas(t *testing.T) {
	for _, kind := range []string{"text", "thinking", "tool_use_input"} {
		t.Run(kind, func(t *testing.T) {
			provider := newCancelAfterDeltaProvider(kind)
			agent, err := NewAgent(AgentOptions{
				Provider:               provider,
				Model:                  "fake-model",
				IncludePartialMessages: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			session, err := agent.Session(context.Background(), SessionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			handle := session.StartRun(context.Background(), "partial", RunOptions{})
			select {
			case <-provider.started:
			case <-time.After(time.Second):
				t.Fatal("provider stream did not start")
			}
			var sawPartial bool
			for ev := range handle.Events() {
				if ev.Type == EventPartialAssistant && ev.Delta["kind"] == kind {
					sawPartial = true
					handle.Close()
					break
				}
			}
			if !sawPartial {
				t.Fatalf("expected %s partial event", kind)
			}
			select {
			case <-provider.ctxDone:
			case <-time.After(time.Second):
				t.Fatal("provider context was not canceled")
			}
			select {
			case <-handle.Done():
			case <-time.After(time.Second):
				t.Fatal("run handle did not finish")
			}
		})
	}
}

type metadataProvider struct {
	mu       sync.Mutex
	requests []core.ProviderRequest
}

func (p *metadataProvider) ID() string { return "metadata" }
func (p *metadataProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *metadataProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
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
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "ok"}
		meta := core.MessageProviderMetadata{}
		if call == 1 {
			meta.OpenAIResponses = &core.OpenAIResponsesMetadata{ResponseID: "resp_1", OutputItems: []map[string]interface{}{{"type": "message"}}}
		}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn, ProviderMetadata: meta}
	}()
	return out, errs
}

func TestProviderMetadataPersistsIntoNextRequest(t *testing.T) {
	provider := &metadataProvider{}
	agent, err := NewAgent(AgentOptions{Provider: provider, Model: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{ID: "meta-session"})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(context.Background(), "first", RunOptions{}) {
	}
	resumed, err := agent.Session(context.Background(), SessionOptions{ID: "meta-session"})
	if err != nil {
		t.Fatal(err)
	}
	for range resumed.Run(context.Background(), "second", RunOptions{}) {
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(provider.requests))
	}
	var found bool
	for _, msg := range provider.requests[1].Messages {
		if msg.ProviderMetadata.OpenAIResponses != nil && msg.ProviderMetadata.OpenAIResponses.ResponseID == "resp_1" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected provider metadata in resumed request")
	}
}

type partialProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *partialProvider) ID() string { return "partial" }
func (p *partialProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *partialProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		if call > 1 {
			out <- core.ProviderStreamEvent{Type: "text_delta", Text: "done"}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
			return
		}
		out <- core.ProviderStreamEvent{Type: "thinking_delta", Text: "think", Signature: "sig"}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "hello"}
		out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: "call_1", Name: "TaskList"}
		out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: "call_1", JSONDelta: `{}`}
		out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: "call_1"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
	}()
	return out, errs
}

func TestPartialAssistantEventsAndThinkingSignature(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		Provider:               &partialProvider{},
		Model:                  "fake-model",
		IncludePartialMessages: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var partials int
	var sawSignature bool
	for ev := range session.Run(context.Background(), "partial", RunOptions{}) {
		if ev.Type == EventPartialAssistant {
			partials++
		}
		if ev.Type == EventAssistant {
			for _, block := range ev.Message.Content {
				if block.Type == BlockThinking && block.Signature == "sig" {
					sawSignature = true
				}
			}
		}
		if ev.Type == EventResult {
			break
		}
	}
	if partials != 4 {
		t.Fatalf("expected 4 partial events, got %d", partials)
	}
	if !sawSignature {
		t.Fatal("expected thinking signature to be preserved")
	}
}

type schedulerProvider struct {
	calls int
}

func (p *schedulerProvider) ID() string { return "scheduler" }
func (p *schedulerProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *schedulerProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.calls++
	call := p.calls
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		if call == 1 {
			for _, id := range []string{"a", "b"} {
				out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: id, Name: "Sleep"}
				out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: id, JSONDelta: `{"ms":120}`}
				out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: id}
			}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "done"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
	}()
	return out, errs
}

type sleepTool struct {
	active    *int32
	maxActive *int32
}

func (t sleepTool) Name() string        { return "Sleep" }
func (t sleepTool) Description() string { return "test sleep tool" }
func (t sleepTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ms": map[string]interface{}{"type": "number"}}}
}
func (t sleepTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (t sleepTool) ParallelSafe() bool    { return true }
func (t sleepTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{"ms": 120}, nil
}
func (t sleepTool) Summarize(input map[string]interface{}) string { return "sleep" }
func (t sleepTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	cur := atomic.AddInt32(t.active, 1)
	for {
		max := atomic.LoadInt32(t.maxActive)
		if cur <= max || atomic.CompareAndSwapInt32(t.maxActive, max, cur) {
			break
		}
	}
	defer atomic.AddInt32(t.active, -1)
	ctx.Emit(core.Event{Type: core.EventUsage, Usage: core.Usage{InputTokens: 1}})
	select {
	case <-time.After(time.Duration(input["ms"].(int)) * time.Millisecond):
	case <-ctx.Context.Done():
		return core.ToolResult{Content: "aborted", Summary: "sleep", IsError: true}, nil
	}
	return core.ToolResult{Content: input["ms"], Summary: "sleep"}, nil
}

func TestParallelSafeToolsRunConcurrentlyAndPreserveResultOrder(t *testing.T) {
	var active, maxActive int32
	reg := tools.NewRegistry()
	if err := reg.Register(sleepTool{active: &active, maxActive: &maxActive}); err != nil {
		t.Fatal(err)
	}
	agent, err := NewAgent(AgentOptions{
		Provider:        &schedulerProvider{},
		Model:           "fake-model",
		Tools:           reg,
		ToolConcurrency: 2,
		Permissions:     PermissionOptions{Mode: PermissionModeYolo},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var sawNestedUsage bool
	var resultIDs []string
	for ev := range session.Run(context.Background(), "schedule", RunOptions{}) {
		if ev.Type == EventUsage && ev.Usage.InputTokens == 1 {
			sawNestedUsage = true
		}
		if ev.Type == EventUser && len(ev.Message.Content) == 2 && ev.Message.Content[0].Type == BlockToolResult {
			for _, block := range ev.Message.Content {
				resultIDs = append(resultIDs, block.ToolUseID)
			}
		}
	}
	if elapsed := time.Since(started); elapsed > 220*time.Millisecond {
		t.Fatalf("expected parallel execution under 220ms, got %s", elapsed)
	}
	if atomic.LoadInt32(&maxActive) < 2 {
		t.Fatal("expected overlapping tool execution")
	}
	if !sawNestedUsage {
		t.Fatal("expected emitted nested event from tool")
	}
	if len(resultIDs) != 2 || resultIDs[0] != "a" || resultIDs[1] != "b" {
		t.Fatalf("expected result order [a b], got %v", resultIDs)
	}
}

type permissionBatchProvider struct {
	calls int
}

func (p *permissionBatchProvider) ID() string { return "permission-batch" }
func (p *permissionBatchProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *permissionBatchProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.calls++
	call := p.calls
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		if call == 1 {
			for _, id := range []string{"p1", "p2"} {
				out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: id, Name: "AskSleep"}
				out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: id, JSONDelta: `{"ms":1}`}
				out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: id}
			}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "done"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
	}()
	return out, errs
}

type askSleepTool struct{}

func (t askSleepTool) Name() string        { return "AskSleep" }
func (t askSleepTool) Description() string { return "write-scoped parallel test tool" }
func (t askSleepTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ms": map[string]interface{}{"type": "number"}}}
}
func (t askSleepTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (t askSleepTool) ParallelSafe() bool    { return true }
func (t askSleepTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{"ms": 1}, nil
}
func (t askSleepTool) Summarize(input map[string]interface{}) string { return "ask sleep" }
func (t askSleepTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{Content: "ok", Summary: "ask sleep"}, nil
}

func TestPermissionRequestsAreBatchedBeforeCallbacks(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(askSleepTool{}); err != nil {
		t.Fatal(err)
	}
	var callbacks int32
	agent, err := NewAgent(AgentOptions{
		Provider: &permissionBatchProvider{},
		Model:    "fake-model",
		Tools:    reg,
		Permissions: PermissionOptions{
			Mode: PermissionModeDefault,
			CanUseTool: func(ctx context.Context, req permissions.CanUseToolRequest) (permissions.CanUseToolResponse, error) {
				atomic.AddInt32(&callbacks, 1)
				return permissions.CanUseToolResponse{Behavior: "allow"}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var sawBatch bool
	var sawToolStartBeforeBatch bool
	for ev := range session.Run(context.Background(), "batch", RunOptions{}) {
		if ev.Type == EventToolCallStart && !sawBatch {
			sawToolStartBeforeBatch = true
		}
		if ev.Type == EventPermissionRequest {
			if len(ev.Requests) != 2 {
				t.Fatalf("expected 2 permission requests in one event, got %d", len(ev.Requests))
			}
			sawBatch = true
		}
	}
	if !sawBatch {
		t.Fatal("expected batched permission request event")
	}
	if sawToolStartBeforeBatch {
		t.Fatal("tool execution started before permission request event")
	}
	if got := atomic.LoadInt32(&callbacks); got != 2 {
		t.Fatalf("expected 2 permission callbacks, got %d", got)
	}
}

type serialSchedulerProvider struct {
	calls int
}

func (p *serialSchedulerProvider) ID() string { return "serial-scheduler" }
func (p *serialSchedulerProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *serialSchedulerProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.calls++
	call := p.calls
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		if call == 1 {
			for _, id := range []string{"s1", "s2"} {
				out <- core.ProviderStreamEvent{Type: "tool_use_start", ID: id, Name: "SerialSleep"}
				out <- core.ProviderStreamEvent{Type: "tool_use_input_delta", ID: id, JSONDelta: `{"ms":80}`}
				out <- core.ProviderStreamEvent{Type: "tool_use_end", ID: id}
			}
			out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopToolUse}
			return
		}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "done"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
	}()
	return out, errs
}

type serialSleepTool struct {
	active    *int32
	maxActive *int32
}

func (t serialSleepTool) Name() string        { return "SerialSleep" }
func (t serialSleepTool) Description() string { return "test serial sleep tool" }
func (t serialSleepTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ms": map[string]interface{}{"type": "number"}}}
}
func (t serialSleepTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (t serialSleepTool) ParallelSafe() bool    { return false }
func (t serialSleepTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{"ms": 80}, nil
}
func (t serialSleepTool) Summarize(input map[string]interface{}) string { return "serial sleep" }
func (t serialSleepTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	cur := atomic.AddInt32(t.active, 1)
	for {
		max := atomic.LoadInt32(t.maxActive)
		if cur <= max || atomic.CompareAndSwapInt32(t.maxActive, max, cur) {
			break
		}
	}
	defer atomic.AddInt32(t.active, -1)
	select {
	case <-time.After(time.Duration(input["ms"].(int)) * time.Millisecond):
	case <-ctx.Context.Done():
		return core.ToolResult{Content: "aborted", Summary: "serial sleep", IsError: true}, nil
	}
	return core.ToolResult{Content: "ok", Summary: "serial sleep"}, nil
}

func TestNonParallelToolsRemainSerialized(t *testing.T) {
	var active, maxActive int32
	reg := tools.NewRegistry()
	if err := reg.Register(serialSleepTool{active: &active, maxActive: &maxActive}); err != nil {
		t.Fatal(err)
	}
	agent, err := NewAgent(AgentOptions{
		Provider:        &serialSchedulerProvider{},
		Model:           "fake-model",
		Tools:           reg,
		ToolConcurrency: 8,
		Permissions:     PermissionOptions{Mode: PermissionModeYolo},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for range session.Run(context.Background(), "serial", RunOptions{}) {
	}
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond {
		t.Fatalf("expected serialized execution to take at least 150ms, got %s", elapsed)
	}
	if atomic.LoadInt32(&maxActive) != 1 {
		t.Fatalf("expected no overlap for serial tools, max active = %d", maxActive)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
