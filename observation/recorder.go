package observation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
)

type Store interface {
	Create(context.Context, Demonstration) error
	Append(context.Context, string, Event) error
	Complete(context.Context, string, map[string]interface{}) (Demonstration, error)
	Get(context.Context, string) (Demonstration, bool, error)
	List(context.Context, string) ([]Demonstration, error)
}

type Recorder struct {
	store     Store
	sanitizer Sanitizer
	now       func() time.Time
}

func NewRecorder(store Store) (*Recorder, error) {
	return NewRecorderWithOptions(RecorderOptions{Store: store})
}

type RecorderOptions struct {
	Store     Store
	Sanitizer Sanitizer
}

func NewRecorderWithOptions(options RecorderOptions) (*Recorder, error) {
	if options.Store == nil {
		return nil, core.NewConfigError("observation recorder requires a store")
	}
	return &Recorder{
		store: options.Store, sanitizer: options.Sanitizer,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (r *Recorder) Start(ctx context.Context, workflowKey string, principal core.Principal, initialContext map[string]interface{}) (Demonstration, error) {
	if workflowKey == "" {
		return Demonstration{}, core.NewConfigError("demonstration workflow key is required")
	}
	contextValue := cloneMap(initialContext)
	if r.sanitizer != nil {
		var err error
		contextValue, err = r.sanitizer.SanitizeMap(
			"initial_context", contextValue,
		)
		if err != nil {
			return Demonstration{}, fmt.Errorf(
				"sanitize demonstration initial context: %w", err,
			)
		}
	}
	demo := Demonstration{
		ID: id.New(), WorkflowKey: workflowKey, Principal: principal,
		Status: DemonstrationRecording, StartedAt: r.now(),
		Trace: WorkflowTrace{
			SchemaVersion: SchemaVersion, SessionID: id.New(),
			InitialContext: contextValue, Events: []Event{},
		},
	}
	if err := r.store.Create(ctx, demo); err != nil {
		return Demonstration{}, err
	}
	return demo, nil
}

func (r *Recorder) Capture(ctx context.Context, demonstrationID string, event Event) (Event, error) {
	demo, ok, err := r.store.Get(ctx, demonstrationID)
	if err != nil {
		return Event{}, err
	}
	if !ok {
		return Event{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: "demonstration not found"}
	}
	if demo.Status != DemonstrationRecording {
		return Event{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "demonstration is not recording"}
	}
	if event.ID == "" {
		event.ID = id.New()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = r.now()
	}
	if event.Sensitivity == "" {
		event.Sensitivity = SensitivityInternal
	}
	event.SchemaVersion = SchemaVersion
	event.SessionID = demo.Trace.SessionID
	if !event.Principal.Valid() {
		event.Principal = demo.Principal
	}
	if event.Action == "" || event.Source == "" || event.Trust == "" {
		return Event{}, core.NewConfigError("observation action, source, and trust are required")
	}
	if event.Principal.TenantID != demo.Principal.TenantID {
		return Event{}, core.NewPermissionError("observation tenant does not match demonstration")
	}
	if r.sanitizer != nil {
		event, err = r.sanitizer.SanitizeEvent(event)
		if err != nil {
			return Event{}, fmt.Errorf("sanitize observation event: %w", err)
		}
	}
	if err := ValidateAppend(demo.Trace, event); err != nil {
		return Event{}, err
	}
	if err := r.store.Append(ctx, demonstrationID, event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (r *Recorder) Complete(ctx context.Context, demonstrationID string, result map[string]interface{}) (Demonstration, error) {
	value := cloneMap(result)
	if r.sanitizer != nil {
		var err error
		value, err = r.sanitizer.SanitizeMap("final_result", value)
		if err != nil {
			return Demonstration{}, fmt.Errorf(
				"sanitize demonstration final result: %w", err,
			)
		}
	}
	return r.store.Complete(ctx, demonstrationID, value)
}

type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]Demonstration
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]Demonstration)}
}

func (s *MemoryStore) Create(ctx context.Context, demonstration Demonstration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, _ := core.PrincipalFromContext(ctx)
	if demonstration.Principal.TenantID != "" && demonstration.Principal.TenantID != principal.TenantID {
		return core.NewPermissionError("demonstration belongs to another tenant")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[demonstration.ID]; exists {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "demonstration already exists"}
	}
	cloned, err := cloneDemonstration(demonstration)
	if err != nil {
		return err
	}
	s.items[demonstration.ID] = cloned
	return nil
}

func (s *MemoryStore) Append(ctx context.Context, demonstrationID string, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	demo, ok := s.items[demonstrationID]
	if !ok {
		return &core.SkawldError{Kind: core.ErrorNotFound, Message: "demonstration not found"}
	}
	principal, _ := core.PrincipalFromContext(ctx)
	if demo.Principal.TenantID != "" && demo.Principal.TenantID != principal.TenantID {
		return core.NewPermissionError("demonstration belongs to another tenant")
	}
	if demo.Status != DemonstrationRecording {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "demonstration is not recording"}
	}
	if err := ValidateAppend(demo.Trace, event); err != nil {
		return err
	}
	demo.Trace.Events = append(demo.Trace.Events, event)
	s.items[demonstrationID] = demo
	return nil
}

func (s *MemoryStore) Complete(ctx context.Context, demonstrationID string, result map[string]interface{}) (Demonstration, error) {
	if err := ctx.Err(); err != nil {
		return Demonstration{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	demo, ok := s.items[demonstrationID]
	if !ok {
		return Demonstration{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: "demonstration not found"}
	}
	principal, _ := core.PrincipalFromContext(ctx)
	if demo.Principal.TenantID != "" && demo.Principal.TenantID != principal.TenantID {
		return Demonstration{}, core.NewPermissionError("demonstration belongs to another tenant")
	}
	if demo.Status != DemonstrationRecording {
		return Demonstration{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "demonstration is not recording"}
	}
	demo.Status = DemonstrationCompleted
	demo.CompletedAt = time.Now().UTC()
	demo.Trace.FinalResult = cloneMap(result)
	if err := demo.Trace.Validate(); err != nil {
		return Demonstration{}, fmt.Errorf("complete demonstration: %w", err)
	}
	s.items[demonstrationID] = demo
	return cloneDemonstration(demo)
}

func (s *MemoryStore) Get(ctx context.Context, demonstrationID string) (Demonstration, bool, error) {
	if err := ctx.Err(); err != nil {
		return Demonstration{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	demo, ok := s.items[demonstrationID]
	if !ok {
		return Demonstration{}, false, nil
	}
	principal, _ := core.PrincipalFromContext(ctx)
	if demo.Principal.TenantID != "" && demo.Principal.TenantID != principal.TenantID {
		return Demonstration{}, false, core.NewPermissionError("demonstration belongs to another tenant")
	}
	cloned, err := cloneDemonstration(demo)
	return cloned, true, err
}

func (s *MemoryStore) List(ctx context.Context, workflowKey string) ([]Demonstration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Demonstration, 0)
	for _, demo := range s.items {
		if workflowKey != "" && demo.WorkflowKey != workflowKey {
			continue
		}
		principal, _ := core.PrincipalFromContext(ctx)
		if demo.Principal.TenantID != "" && demo.Principal.TenantID != principal.TenantID {
			continue
		}
		cloned, err := cloneDemonstration(demo)
		if err != nil {
			return nil, err
		}
		out = append(out, cloned)
	}
	return out, nil
}

func cloneDemonstration(input Demonstration) (Demonstration, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return Demonstration{}, err
	}
	var output Demonstration
	if err := json.Unmarshal(raw, &output); err != nil {
		return Demonstration{}, err
	}
	return output, nil
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	raw, _ := json.Marshal(input)
	var output map[string]interface{}
	_ = json.Unmarshal(raw, &output)
	return output
}
