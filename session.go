package skawld

import (
	"context"
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

	agent        *Agent
	store        core.SessionStore
	providerMu   sync.Mutex
	providerView []core.Message
	fullHistory  []core.Message
	readTracker  *tools.FileReadTracker
	activeMu     sync.Mutex
	active       bool
	cancelActive context.CancelFunc
	lastUsage    core.Usage
}

func newSession(agent *Agent, rec core.SessionRecord, providerView []core.Message) *Session {
	created, _ := time.Parse(time.RFC3339Nano, rec.CreatedAt)
	return &Session{
		ID: rec.ID, CreatedAt: created, Meta: rec.Meta,
		agent: agent, store: agent.store, providerView: providerView,
		fullHistory: append([]core.Message(nil), providerView...),
		readTracker: tools.NewFileReadTracker(),
	}
}

func (s *Session) MessageCount() int {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	return len(s.providerView)
}

func (s *Session) Run(ctx context.Context, prompt string, opts RunOptions) <-chan core.Event {
	out := make(chan core.Event, 64)
	s.activeMu.Lock()
	if s.active {
		s.activeMu.Unlock()
		go func() {
			defer close(out)
			out <- core.Event{Type: core.EventError, Error: &core.EventErrorPayload{Name: "ConfigError", Message: "Session already has an active run"}}
			out <- core.Event{Type: core.EventResult, Subtype: "error", StopReason: core.StopError}
		}()
		return out
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.active = true
	s.cancelActive = cancel
	s.activeMu.Unlock()
	go func() {
		defer close(out)
		defer func() {
			cancel()
			s.activeMu.Lock()
			s.active = false
			s.cancelActive = nil
			s.activeMu.Unlock()
		}()
		s.runLoop(runCtx, prompt, opts, out)
	}()
	return out
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

func (s *Session) UpdateMeta(meta map[string]interface{}) error {
	rec, err := s.store.UpdateMeta(s.ID, meta)
	if err != nil {
		return err
	}
	s.Meta = rec.Meta
	return nil
}
