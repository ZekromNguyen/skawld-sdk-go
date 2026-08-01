package skawld

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
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

// Session holds one conversation history. A Session permits one active run at
// a time; metadata and run lifecycle methods are safe to call from concurrent
// goroutines.
type Session struct {
	ID        string
	CreatedAt time.Time
	Principal core.Principal
	// Meta is a compatibility snapshot. Use Metadata for a concurrency-safe
	// copy when sessions may be accessed by multiple goroutines.
	Meta map[string]interface{}

	agent               *Agent
	store               core.SessionStore
	providerMu          sync.Mutex
	providerHistory     []core.Message
	providerChars       int
	completeHistory     []core.Message
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
	problemState        *problemRunState
	metaMu              sync.RWMutex
	metadata            map[string]interface{}
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

func newSession(agent *Agent, rec core.SessionRecord, providerHistory []core.Message, initialEvents []core.Event, principal core.Principal) *Session {
	created, _ := time.Parse(time.RFC3339Nano, rec.CreatedAt)
	if !principal.Valid() {
		principal = core.PrincipalFromSessionMeta(rec.Meta)
	}
	meta := cloneMeta(rec.Meta)
	return &Session{
		ID: rec.ID, CreatedAt: created, Principal: principal, Meta: cloneMeta(meta), metadata: meta,
		agent: agent, store: agent.store, providerHistory: providerHistory, providerChars: estimateMessagesProviderChars(providerHistory),
		completeHistory: append([]core.Message(nil), providerHistory...), invokedSkills: append([]core.InvokedSkillRecord(nil), rec.InvokedSkills...),
		initialEvents: append([]core.Event(nil), initialEvents...),
		readTracker:   tools.NewFileReadTracker(),
		problemState:  newProblemRunState(),
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
	return len(s.providerHistory)
}

func (s *Session) Run(ctx context.Context, prompt string, opts RunOptions) <-chan core.Event {
	return s.StartRun(ctx, prompt, opts).Events()
}

func (s *Session) StartRun(ctx context.Context, prompt string, opts RunOptions) *RunHandle {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.agent.production() != nil {
		authenticated, exists := core.PrincipalFromContext(ctx)
		if !exists || !authenticated.Authenticated() {
			return rejectedRunHandle(
				ctx,
				"PermissionError",
				"production run requires an authenticated context identity",
			)
		}
		if authenticated.TenantID != s.Principal.TenantID ||
			authenticated.ActorID != s.Principal.ActorID {
			return rejectedRunHandle(
				ctx,
				"PermissionError",
				"production run identity does not match the session identity",
			)
		}
		// Use the immutable trusted claims captured when the session was opened.
		// A later caller cannot add roles by replacing the context principal.
		ctx = core.WithPrincipal(ctx, s.Principal)
	} else if s.Principal.Valid() {
		ctx = core.WithPrincipal(ctx, s.Principal)
	}
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
	if !s.agent.beginRun() {
		s.activeMu.Unlock()
		emitCtx, cancelEmit := context.WithCancel(ctx)
		handle.abort = func() {}
		handle.close = cancelEmit
		go func() {
			defer close(done)
			defer close(out)
			defer cancelEmit()
			emitter := newEventEmitter(emitCtx, out)
			_ = emitter.Emit(core.Event{Type: core.EventError, Error: &core.EventErrorPayload{Name: "ConfigError", Message: "Agent is closed"}})
			_ = emitter.Emit(core.Event{Type: core.EventResult, Subtype: "error", StopReason: core.StopError})
		}()
		return handle
	}
	runCtx, cancel := context.WithCancel(ctx)
	if production := s.agent.production(); production != nil {
		cancel()
		runCtx, cancel = context.WithTimeout(
			ctx, production.Limits.MaxRunDuration,
		)
	}
	emitCtx, cancelEmit := context.WithCancel(ctx)
	stopAgentCancel := context.AfterFunc(s.agent.lifecycleCtx, func() {
		cancel()
		cancelEmit()
	})
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
			stopAgentCancel()
			cancel()
			cancelEmit()
			s.activeMu.Lock()
			s.active = false
			s.cancelActive = nil
			s.activeMu.Unlock()
			s.agent.endRun()
		}()
		s.runLoop(runCtx, prompt, opts, newEventEmitter(emitCtx, out))
	}()
	return handle
}

func rejectedRunHandle(
	ctx context.Context,
	name string,
	message string,
) *RunHandle {
	emitCtx, cancelEmit := context.WithCancel(ctx)
	out := make(chan core.Event, 2)
	done := make(chan struct{})
	handle := &RunHandle{
		events: out,
		abort:  func() {},
		close:  cancelEmit,
		done:   done,
	}
	go func() {
		defer close(done)
		defer close(out)
		defer cancelEmit()
		emitter := newEventEmitter(emitCtx, out)
		_ = emitter.Emit(core.Event{
			Type: core.EventError,
			Error: &core.EventErrorPayload{
				Name: name, Message: message,
			},
		})
		_ = emitter.Emit(core.Event{
			Type: core.EventResult, Subtype: "error",
			StopReason: core.StopError,
		})
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

func (s *Session) append(ctx context.Context, messages []core.Message) error {
	// Hold providerMu across the budget check, the durable write, and the
	// in-memory update so two concurrent appends cannot both pass the
	// MaxSessionBytes check and jointly exceed the budget (TOCTOU).
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if production := s.agent.production(); production != nil {
		incoming := estimateMessagesProviderChars(messages)
		current := estimateMessagesProviderChars(s.completeHistory)
		if current+incoming > production.Limits.MaxSessionBytes {
			return &core.SkawldError{
				Kind: core.ErrorValidation,
				Message: fmt.Sprintf(
					"session history exceeds production limit of %d bytes",
					production.Limits.MaxSessionBytes,
				),
			}
		}
	}
	if _, err := s.store.AppendMessages(ctx, s.ID, messages); err != nil {
		return err
	}
	s.providerHistory = append(s.providerHistory, messages...)
	s.providerChars += estimateMessagesProviderChars(messages)
	s.completeHistory = append(s.completeHistory, messages...)
	return nil
}

func (s *Session) compactProviderHistory(
	ctx context.Context,
	runID string,
	trigger string,
	emitter *eventEmitter,
	used core.Usage,
) (bool, core.Usage, error) {
	strategy := s.agent.opts.CompactionStrategy
	if s.agent.opts.DisableCompaction || strategy == nil {
		return false, core.Usage{}, nil
	}
	system := s.agent.systemBlocks()
	tools := s.agent.toolSchemas()
	if trigger == compactionTriggerProactive {
		estimated := s.estimatedProviderTokens()
		if !s.shouldCompactProactively(estimated) {
			return false, core.Usage{}, nil
		}
	}
	s.providerMu.Lock()
	messages := cloneMessages(s.providerHistory)
	s.providerMu.Unlock()
	messages = stripProviderOnlyCompactionMessages(messages)
	tokensBefore := estimateProviderTokens(system, tools, messages)
	if trigger == compactionTriggerProactive && !s.shouldCompactProactively(tokensBefore) {
		return false, core.Usage{}, nil
	}
	provider := s.agent.opts.Provider
	var guard *productionCompactionProvider
	if production := s.agent.production(); production != nil {
		var err error
		guard, err = newProductionCompactionProvider(
			provider, production.Limits, used,
		)
		if err != nil {
			return false, core.Usage{}, err
		}
		provider = guard
	}
	start := time.Now()
	result, err := strategy.Compact(ctx, CompactionRequest{
		Provider:        provider,
		Model:           s.agent.opts.Model,
		System:          system,
		Tools:           tools,
		Messages:        messages,
		Trigger:         trigger,
		ContextWindow:   s.agent.opts.Provider.ContextWindow(s.agent.opts.Model),
		EstimatedTokens: tokensBefore,
	})
	compactionUsage := core.Usage{}
	if guard != nil {
		compactionUsage = guard.Usage()
	}
	s.agent.observe(ctx, core.Observation{
		Type:       core.ObservationCompaction,
		Operation:  trigger,
		SessionID:  s.ID,
		RunID:      runID,
		ProviderID: s.agent.opts.Provider.ID(),
		DurationMS: time.Since(start).Milliseconds(),
		Error:      err,
	})
	if err != nil {
		return false, compactionUsage, fmt.Errorf(
			"compact provider view for session %q: %w", s.ID, err,
		)
	}
	if !result.Changed {
		return false, compactionUsage, nil
	}
	next := stripProviderOnlyCompactionMessages(cloneMessages(result.Messages))
	skills, err := s.loadInvokedSkills(ctx)
	if err != nil {
		return false, compactionUsage, err
	}
	next = injectSkillReplayMessages(next, skills)
	tokensAfter := estimateProviderTokens(system, tools, next)
	s.providerMu.Lock()
	s.providerHistory = next
	s.providerChars = estimateMessagesProviderChars(next)
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
		return false, compactionUsage,
			core.NewAbortError("run event stream closed", ctx.Err())
	}
	return true, compactionUsage, nil
}

func (s *Session) loadInvokedSkills(ctx context.Context) ([]core.InvokedSkillRecord, error) {
	rec, ok, err := s.store.Load(ctx, s.ID)
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
	chars := estimateStaticProviderChars(system, tools)
	chars += estimateMessagesProviderChars(messages)
	return tokensFromChars(chars)
}

func (s *Session) estimatedProviderTokens() int {
	s.providerMu.Lock()
	messageChars := s.providerChars
	s.providerMu.Unlock()
	return tokensFromChars(s.agent.staticProviderChars() + messageChars)
}

func estimateStaticProviderChars(system []core.SystemBlock, tools []core.ToolSchema) int {
	chars := 0
	for _, block := range system {
		chars += len(block.Text) + 16
	}
	for _, tool := range tools {
		raw, _ := json.Marshal(tool)
		chars += len(raw)
	}
	return chars
}

func estimateMessagesProviderChars(messages []core.Message) int {
	chars := 0
	for _, msg := range messages {
		raw, _ := json.Marshal(msg)
		chars += len(raw)
	}
	return chars
}

func tokensFromChars(chars int) int {
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
	return s.UpdateMetaContext(context.Background(), meta)
}

func (s *Session) UpdateMetaContext(ctx context.Context, meta map[string]interface{}) error {
	if s.Principal.Valid() {
		ctx = core.WithPrincipal(ctx, s.Principal)
	}
	bound, ok := core.BindPrincipalToSessionMeta(meta, s.Principal)
	if !ok {
		return core.NewConfigError("session metadata conflicts with authenticated principal")
	}
	rec, err := s.store.UpdateMeta(ctx, s.ID, bound)
	if err != nil {
		return err
	}
	s.metaMu.Lock()
	s.metadata = cloneMeta(rec.Meta)
	s.Meta = cloneMeta(rec.Meta)
	s.metaMu.Unlock()
	return nil
}

// Metadata returns an isolated copy of session metadata.
func (s *Session) Metadata() map[string]interface{} {
	s.metaMu.RLock()
	defer s.metaMu.RUnlock()
	return cloneMeta(s.metadata)
}

func cloneMeta(meta map[string]interface{}) map[string]interface{} {
	if meta == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(meta))
	for key, value := range meta {
		out[key] = value
	}
	return out
}
