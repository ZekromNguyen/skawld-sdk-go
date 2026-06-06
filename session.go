package skawld

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/tools"
)

type RunImage struct {
	Data      string
	MediaType string
	URL       string
}

type RunOptions struct {
	MaxOutputTokens *int
	Temperature     *float64
	Images          []RunImage
	Thinking        map[string]interface{}
	Effort          string
}

type Session struct {
	ID        string
	CreatedAt time.Time
	Meta      map[string]interface{}

	agent               *Agent
	store               core.SessionStore
	providerMu          sync.Mutex
	providerView        []core.Message
	fullHistory         []core.Message
	invokedSkills       []core.InvokedSkillRecord
	initialEvents       []core.Event
	pendingSkillOverlay *skillOverlay
	activeSkillOverlay  *skillOverlay
	skillMu             sync.Mutex
	readTracker         *tools.FileReadTracker
	activeMu            sync.Mutex
	active              bool
	cancelActive        context.CancelFunc
	lastUsage           core.Usage
}

type skillOverlay struct {
	Name         string
	Body         string
	Model        core.ModelID
	AllowedTools []string
	Arguments    string
	InvokedAt    int64
}

type RunHandle struct {
	events <-chan core.Event
	abort  context.CancelFunc
	close  context.CancelFunc
	done   <-chan struct{}
	once   sync.Once
}

func (h *RunHandle) Events() <-chan core.Event {
	if h == nil {
		return nil
	}
	return h.events
}

func (h *RunHandle) Abort() {
	if h == nil || h.abort == nil {
		return
	}
	h.abort()
}

func (h *RunHandle) Close() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if h.close != nil {
			h.close()
		} else if h.abort != nil {
			h.abort()
		}
	})
}

func (h *RunHandle) Done() <-chan struct{} {
	if h == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return h.done
}

func newSession(agent *Agent, rec core.SessionRecord, providerView []core.Message, initialEvents []core.Event) *Session {
	created, _ := time.Parse(time.RFC3339Nano, rec.CreatedAt)
	return &Session{
		ID: rec.ID, CreatedAt: created, Meta: rec.Meta,
		agent: agent, store: agent.store, providerView: providerView,
		fullHistory: append([]core.Message(nil), providerView...), invokedSkills: append([]core.InvokedSkillRecord(nil), rec.InvokedSkills...),
		initialEvents: append([]core.Event(nil), initialEvents...),
		readTracker:   tools.NewFileReadTracker(),
	}
}

func (s *Session) consumeInitialEvents() []core.Event {
	s.skillMu.Lock()
	defer s.skillMu.Unlock()
	events := append([]core.Event(nil), s.initialEvents...)
	s.initialEvents = nil
	return events
}

func (s *Session) MessageCount() int {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	return len(s.providerView)
}

func (s *Session) Run(ctx context.Context, prompt string, opts RunOptions) <-chan core.Event {
	return s.StartRun(ctx, prompt, opts).Events()
}

func (s *Session) StartRun(ctx context.Context, prompt string, opts RunOptions) *RunHandle {
	out := make(chan core.Event, 64)
	done := make(chan struct{})
	handle := &RunHandle{events: out, done: done}
	s.activeMu.Lock()
	if s.active {
		s.activeMu.Unlock()
		emitCtx, cancelEmit := context.WithCancel(ctx)
		handle.abort = func() {}
		handle.close = cancelEmit
		go func() {
			defer close(done)
			defer close(out)
			defer cancelEmit()
			emitter := newEventEmitter(emitCtx, out)
			_ = emitter.Emit(core.Event{Type: core.EventError, Error: &core.EventErrorPayload{Name: "ConfigError", Message: "Session already has an active run"}})
			_ = emitter.Emit(core.Event{Type: core.EventResult, Subtype: "error", StopReason: core.StopError})
		}()
		return handle
	}
	runCtx, cancel := context.WithCancel(ctx)
	emitCtx, cancelEmit := context.WithCancel(ctx)
	handle.abort = cancel
	handle.close = func() {
		cancelEmit()
		cancel()
	}
	s.active = true
	s.cancelActive = cancel
	s.activeMu.Unlock()
	go func() {
		defer close(done)
		defer close(out)
		defer func() {
			cancel()
			cancelEmit()
			s.activeMu.Lock()
			s.active = false
			s.cancelActive = nil
			s.activeMu.Unlock()
		}()
		s.runLoop(runCtx, prompt, opts, newEventEmitter(emitCtx, out))
	}()
	return handle
}

func (s *Session) Abort() {
	s.activeMu.Lock()
	cancel := s.cancelActive
	s.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Session) append(messages []core.Message) error {
	if _, err := s.store.AppendMessages(s.ID, messages); err != nil {
		return err
	}
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	s.providerView = append(s.providerView, messages...)
	s.fullHistory = append(s.fullHistory, messages...)
	return nil
}

func (s *Session) compactProviderView(ctx context.Context, trigger string, emitter *eventEmitter) (bool, error) {
	strategy := s.agent.opts.CompactionStrategy
	if s.agent.opts.DisableCompaction || strategy == nil {
		return false, nil
	}
	s.providerMu.Lock()
	messages := cloneMessages(s.providerView)
	s.providerMu.Unlock()
	messages = stripProviderOnlyCompactionMessages(messages)
	tokensBefore := estimateProviderTokens(s.agent.system, s.agent.opts.Tools.Schemas(), messages)
	if trigger == compactionTriggerProactive && !s.shouldCompactProactively(tokensBefore) {
		return false, nil
	}
	result, err := strategy.Compact(ctx, CompactionRequest{
		Provider:        s.agent.opts.Provider,
		Model:           s.agent.opts.Model,
		System:          append([]core.SystemBlock(nil), s.agent.system...),
		Tools:           s.agent.opts.Tools.Schemas(),
		Messages:        messages,
		Trigger:         trigger,
		ContextWindow:   s.agent.opts.Provider.ContextWindow(s.agent.opts.Model),
		EstimatedTokens: tokensBefore,
	})
	if err != nil || !result.Changed {
		return false, err
	}
	next := stripProviderOnlyCompactionMessages(cloneMessages(result.Messages))
	skills, err := s.loadInvokedSkills()
	if err != nil {
		return false, err
	}
	next = injectSkillReplayMessages(next, skills)
	tokensAfter := estimateProviderTokens(s.agent.system, s.agent.opts.Tools.Schemas(), next)
	s.providerMu.Lock()
	s.providerView = next
	s.providerMu.Unlock()
	if !emitter.Emit(core.Event{
		Type:           core.EventCompaction,
		Subtype:        trigger,
		MessagesBefore: len(messages),
		MessagesAfter:  len(next),
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		Strategy:       strategy.Name(),
	}) {
		return false, core.NewAbortError("run event stream closed", ctx.Err())
	}
	return true, nil
}

func (s *Session) loadInvokedSkills() ([]core.InvokedSkillRecord, error) {
	rec, ok, err := s.store.Load(s.ID)
	if err != nil {
		return nil, err
	}
	if ok {
		s.invokedSkills = append([]core.InvokedSkillRecord(nil), rec.InvokedSkills...)
	}
	return append([]core.InvokedSkillRecord(nil), s.invokedSkills...), nil
}

func (s *Session) shouldCompactProactively(tokensBefore int) bool {
	window := s.agent.opts.Provider.ContextWindow(s.agent.opts.Model)
	if window <= 0 || tokensBefore <= 0 {
		return false
	}
	threshold := s.agent.opts.CompactionThreshold
	if threshold <= 0 {
		threshold = defaultCompactionThreshold
	}
	return float64(tokensBefore) >= float64(window)*threshold
}

func estimateProviderTokens(system []core.SystemBlock, tools []core.ToolSchema, messages []core.Message) int {
	chars := 0
	for _, block := range system {
		chars += len(block.Text) + 16
	}
	for _, tool := range tools {
		raw, _ := json.Marshal(tool)
		chars += len(raw)
	}
	for _, msg := range messages {
		raw, _ := json.Marshal(msg)
		chars += len(raw)
	}
	if chars == 0 {
		return 0
	}
	tokens := chars / 4
	if chars%4 != 0 {
		tokens++
	}
	return tokens
}

func (s *Session) UpdateMeta(meta map[string]interface{}) error {
	rec, err := s.store.UpdateMeta(s.ID, meta)
	if err != nil {
		return err
	}
	s.Meta = rec.Meta
	return nil
}
