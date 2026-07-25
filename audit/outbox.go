package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// Delivery is a durable audit event awaiting delivery to an operational sink.
// Event is immutable after enqueue.
type Delivery struct {
	Event          Event     `json:"event"`
	Attempts       int       `json:"attempts"`
	CreatedAt      time.Time `json:"created_at"`
	LastAttemptAt  time.Time `json:"last_attempt_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
	NextAttemptAt  time.Time `json:"next_attempt_at,omitempty"`
	LeaseOwner     string    `json:"lease_owner,omitempty"`
	LeaseUntil     time.Time `json:"lease_until,omitempty"`
	DeadLetteredAt time.Time `json:"dead_lettered_at,omitempty"`
}

// Outbox stores audit events before best-effort delivery. Implementations must
// make Enqueue idempotent by event ID.
type Outbox interface {
	Enqueue(context.Context, Event) error
	Pending(context.Context, int) ([]Delivery, error)
	MarkAttempt(context.Context, string, string) error
	MarkDelivered(context.Context, string) error
}

type MemoryOutbox struct {
	mu    sync.RWMutex
	items map[string]Delivery
	now   func() time.Time
}

func NewMemoryOutbox() *MemoryOutbox {
	return &MemoryOutbox{
		items: make(map[string]Delivery),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *MemoryOutbox) Enqueue(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := authorizeEvent(ctx, event); err != nil {
		return err
	}
	if err := validateEvent(event); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, exists := s.items[event.ID]; exists {
		if existing.Event.TenantID == event.TenantID &&
			equalEvent(existing.Event, event) {
			return nil
		}
		if existing.Event.TenantID != event.TenantID {
			return core.NewPermissionError(
				"audit outbox event belongs to another tenant",
			)
		}
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "audit outbox event id already has different content",
		}
	}
	s.items[event.ID] = Delivery{
		Event: cloneEvent(event), CreatedAt: s.now(),
	}
	return nil
}

func (s *MemoryOutbox) Pending(
	ctx context.Context,
	limit int,
) ([]Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	principal, err := outboxPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, core.NewConfigError(
			"audit outbox pending limit must be between 1 and 1000",
		)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Delivery, 0)
	for _, delivery := range s.items {
		if delivery.Event.TenantID == principal.TenantID &&
			delivery.DeliveredAt.IsZero() &&
			delivery.DeadLetteredAt.IsZero() {
			delivery.Event = cloneEvent(delivery.Event)
			output = append(output, delivery)
		}
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].CreatedAt.Equal(output[j].CreatedAt) {
			return output[i].Event.ID < output[j].Event.ID
		}
		return output[i].CreatedAt.Before(output[j].CreatedAt)
	})
	if len(output) > limit {
		output = output[:limit]
	}
	return output, nil
}

func (s *MemoryOutbox) MarkAttempt(
	ctx context.Context,
	eventID string,
	message string,
) error {
	return s.update(ctx, eventID, false, message)
}

func (s *MemoryOutbox) MarkDelivered(
	ctx context.Context,
	eventID string,
) error {
	return s.update(ctx, eventID, true, "")
}

func (s *MemoryOutbox) update(
	ctx context.Context,
	eventID string,
	delivered bool,
	message string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, err := outboxPrincipal(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, exists := s.items[eventID]
	if !exists {
		return &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "audit outbox event not found",
		}
	}
	if item.Event.TenantID != principal.TenantID {
		return core.NewPermissionError("audit outbox event belongs to another tenant")
	}
	if !item.DeliveredAt.IsZero() {
		return nil
	}
	item.Attempts++
	item.LastAttemptAt = s.now()
	item.LastError = boundedError(message)
	if delivered {
		item.DeliveredAt = item.LastAttemptAt
		item.LastError = ""
	}
	s.items[eventID] = item
	return nil
}

// Dispatcher durably enqueues before attempting delivery. Once enqueue
// succeeds, a downstream failure does not fail the business operation; the
// event remains pending for Flush.
type Dispatcher struct {
	outbox Outbox
	sink   Sink
}

func NewDispatcher(outbox Outbox, sink Sink) (*Dispatcher, error) {
	if outbox == nil {
		return nil, core.NewConfigError("audit dispatcher requires an outbox")
	}
	return &Dispatcher{outbox: outbox, sink: sink}, nil
}

func (d *Dispatcher) Append(ctx context.Context, event Event) error {
	if err := d.outbox.Enqueue(ctx, event); err != nil {
		return err
	}
	if d.sink == nil {
		return nil
	}
	if err := d.sink.Append(ctx, event); err != nil {
		_ = d.outbox.MarkAttempt(
			context.WithoutCancel(ctx), event.ID, err.Error(),
		)
		return nil
	}
	_ = d.outbox.MarkDelivered(context.WithoutCancel(ctx), event.ID)
	return nil
}

// Flush retries pending events in deterministic enqueue order. Delivery sinks
// should be idempotent by event ID.
func (d *Dispatcher) Flush(ctx context.Context, limit int) error {
	if d.sink == nil {
		return core.NewConfigError("audit dispatcher has no delivery sink")
	}
	pending, err := d.outbox.Pending(ctx, limit)
	if err != nil {
		return err
	}
	var failures []error
	for _, delivery := range pending {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := d.sink.Append(ctx, delivery.Event); err != nil {
			_ = d.outbox.MarkAttempt(
				context.WithoutCancel(ctx), delivery.Event.ID, err.Error(),
			)
			failures = append(failures, fmt.Errorf(
				"deliver audit event %q: %w", delivery.Event.ID, err,
			))
			continue
		}
		if err := d.outbox.MarkDelivered(ctx, delivery.Event.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func validateEvent(event Event) error {
	if strings.TrimSpace(event.ID) == "" ||
		strings.TrimSpace(string(event.Type)) == "" ||
		event.Timestamp.IsZero() ||
		strings.TrimSpace(event.TenantID) == "" {
		return core.NewConfigError(
			"audit outbox event requires id, type, timestamp, and tenant",
		)
	}
	return nil
}

func authorizeEvent(ctx context.Context, event Event) error {
	principal, err := outboxPrincipal(ctx)
	if err != nil {
		return err
	}
	if principal.TenantID != event.TenantID {
		return core.NewPermissionError("audit outbox event belongs to another tenant")
	}
	return nil
}

func outboxPrincipal(ctx context.Context) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || !principal.Authenticated() {
		return core.Principal{}, core.NewPermissionError(
			"audit outbox requires authenticated tenant and actor identities",
		)
	}
	return principal, nil
}

func boundedError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}

func cloneEvent(event Event) Event {
	event.Attributes = cloneMap(event.Attributes)
	return event
}

func equalEvent(left, right Event) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil &&
		string(leftJSON) == string(rightJSON)
}
