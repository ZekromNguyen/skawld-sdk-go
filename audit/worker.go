package audit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type LeaseRequest struct {
	WorkerID      string
	Limit         int
	LeaseDuration time.Duration
	Now           time.Time
}

type DeliveryFailure struct {
	WorkerID      string
	At            time.Time
	Error         string
	NextAttemptAt time.Time
	DeadLetter    bool
}

// LeasedOutbox coordinates multiple delivery workers. Claim must atomically
// exclude live leases held by another worker.
type LeasedOutbox interface {
	Outbox
	Claim(context.Context, LeaseRequest) ([]Delivery, error)
	Acknowledge(context.Context, string, string, time.Time) error
	Fail(context.Context, string, DeliveryFailure) error
	DeadLetters(context.Context, int) ([]Delivery, error)
	Requeue(context.Context, string, time.Time) error
}

type WorkerOptions struct {
	Outbox        LeasedOutbox
	Sink          Sink
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	MaxAttempts   int
	Now           func() time.Time
	OnError       func(error)
}

type Worker struct {
	outbox        LeasedOutbox
	sink          Sink
	workerID      string
	batchSize     int
	leaseDuration time.Duration
	baseBackoff   time.Duration
	maxBackoff    time.Duration
	maxAttempts   int
	now           func() time.Time
	onError       func(error)
}

type WorkerResult struct {
	Claimed      int
	Delivered    int
	Failed       int
	DeadLettered int
}

func NewWorker(options WorkerOptions) (*Worker, error) {
	if options.Outbox == nil || options.Sink == nil {
		return nil, core.NewConfigError(
			"audit worker requires a leased outbox and delivery sink",
		)
	}
	options.WorkerID = strings.TrimSpace(options.WorkerID)
	if options.WorkerID == "" || len(options.WorkerID) > 256 ||
		strings.ContainsAny(options.WorkerID, "\r\n\x00") {
		return nil, core.NewConfigError("audit worker id is invalid")
	}
	if options.BatchSize == 0 {
		options.BatchSize = 100
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 {
		return nil, core.NewConfigError(
			"audit worker batch size must be between 1 and 1000",
		)
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = time.Minute
	}
	if options.LeaseDuration < time.Second ||
		options.LeaseDuration > 24*time.Hour {
		return nil, core.NewConfigError(
			"audit worker lease duration must be between one second and 24 hours",
		)
	}
	if options.BaseBackoff == 0 {
		options.BaseBackoff = time.Second
	}
	if options.MaxBackoff == 0 {
		options.MaxBackoff = time.Hour
	}
	if options.BaseBackoff < time.Millisecond ||
		options.MaxBackoff < options.BaseBackoff {
		return nil, core.NewConfigError(
			"audit worker backoff configuration is invalid",
		)
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = 10
	}
	if options.MaxAttempts < 1 || options.MaxAttempts > 1000 {
		return nil, core.NewConfigError(
			"audit worker max attempts must be between 1 and 1000",
		)
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Worker{
		outbox: options.Outbox, sink: options.Sink,
		workerID: options.WorkerID, batchSize: options.BatchSize,
		leaseDuration: options.LeaseDuration,
		baseBackoff:   options.BaseBackoff, maxBackoff: options.MaxBackoff,
		maxAttempts: options.MaxAttempts, now: options.Now,
		onError: options.OnError,
	}, nil
}

func (w *Worker) RunOnce(
	ctx context.Context,
) (WorkerResult, error) {
	now := w.now()
	deliveries, err := w.outbox.Claim(ctx, LeaseRequest{
		WorkerID: w.workerID, Limit: w.batchSize,
		LeaseDuration: w.leaseDuration, Now: now,
	})
	if err != nil {
		return WorkerResult{}, err
	}
	result := WorkerResult{Claimed: len(deliveries)}
	failures := make([]error, 0)
	for _, delivery := range deliveries {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		if err := w.sink.Append(ctx, delivery.Event); err != nil {
			result.Failed++
			attempt := delivery.Attempts + 1
			dead := attempt >= w.maxAttempts
			next := time.Time{}
			if dead {
				result.DeadLettered++
			} else {
				next = w.now().Add(w.backoff(attempt))
			}
			if failErr := w.outbox.Fail(
				context.WithoutCancel(ctx), delivery.Event.ID,
				DeliveryFailure{
					WorkerID: w.workerID, At: w.now(),
					Error: err.Error(), NextAttemptAt: next,
					DeadLetter: dead,
				},
			); failErr != nil {
				failures = append(failures, failErr)
			}
			failures = append(failures, fmt.Errorf(
				"deliver audit event %q: %w", delivery.Event.ID, err,
			))
			continue
		}
		if err := w.outbox.Acknowledge(
			ctx, delivery.Event.ID, w.workerID, w.now(),
		); err != nil {
			failures = append(failures, err)
			continue
		}
		result.Delivered++
	}
	return result, errors.Join(failures...)
}

// Run polls until cancellation. Delivery failures are reported to OnError and
// remain scheduled in the outbox. Without OnError, the first operational
// error terminates the worker.
func (w *Worker) Run(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval < 10*time.Millisecond ||
		pollInterval > time.Hour {
		return core.NewConfigError(
			"audit worker poll interval must be between 10ms and one hour",
		)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			_, err := w.RunOnce(ctx)
			if err != nil {
				if w.onError == nil {
					return err
				}
				w.onError(err)
			}
			timer.Reset(pollInterval)
		}
	}
}

func (w *Worker) backoff(attempt int) time.Duration {
	delay := w.baseBackoff
	for index := 1; index < attempt; index++ {
		if delay >= w.maxBackoff/2 {
			return w.maxBackoff
		}
		delay *= 2
	}
	if delay > w.maxBackoff {
		return w.maxBackoff
	}
	return delay
}

func validateLeaseRequest(request LeaseRequest) error {
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	if request.WorkerID == "" || len(request.WorkerID) > 256 ||
		strings.ContainsAny(request.WorkerID, "\r\n\x00") {
		return core.NewConfigError("audit outbox worker id is invalid")
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return core.NewConfigError(
			"audit outbox claim limit must be between 1 and 1000",
		)
	}
	if request.LeaseDuration < time.Second ||
		request.LeaseDuration > 24*time.Hour ||
		request.Now.IsZero() {
		return core.NewConfigError(
			"audit outbox claim requires a valid time and lease duration",
		)
	}
	return nil
}

func validateDeliveryFailure(failure DeliveryFailure) error {
	if strings.TrimSpace(failure.WorkerID) == "" ||
		failure.At.IsZero() {
		return core.NewConfigError(
			"audit delivery failure requires worker and timestamp",
		)
	}
	if !failure.DeadLetter &&
		(failure.NextAttemptAt.IsZero() ||
			failure.NextAttemptAt.Before(failure.At)) {
		return core.NewConfigError(
			"audit delivery failure requires a future retry time",
		)
	}
	return nil
}

func (s *MemoryOutbox) Claim(
	ctx context.Context,
	request LeaseRequest,
) ([]Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateLeaseRequest(request); err != nil {
		return nil, err
	}
	principal, err := outboxPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates := make([]Delivery, 0)
	for _, delivery := range s.items {
		if delivery.Event.TenantID != principal.TenantID ||
			!delivery.DeliveredAt.IsZero() ||
			!delivery.DeadLetteredAt.IsZero() ||
			!delivery.NextAttemptAt.IsZero() &&
				delivery.NextAttemptAt.After(request.Now) ||
			!delivery.LeaseUntil.IsZero() &&
				delivery.LeaseUntil.After(request.Now) {
			continue
		}
		candidates = append(candidates, delivery)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].Event.ID < candidates[j].Event.ID
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	if len(candidates) > request.Limit {
		candidates = candidates[:request.Limit]
	}
	for index := range candidates {
		delivery := s.items[candidates[index].Event.ID]
		delivery.LeaseOwner = request.WorkerID
		delivery.LeaseUntil = request.Now.Add(request.LeaseDuration)
		s.items[delivery.Event.ID] = delivery
		delivery.Event = cloneEvent(delivery.Event)
		candidates[index] = delivery
	}
	return candidates, nil
}

func (s *MemoryOutbox) Acknowledge(
	ctx context.Context,
	eventID string,
	workerID string,
	at time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, err := outboxPrincipal(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(workerID) == "" || at.IsZero() {
		return core.NewConfigError(
			"audit acknowledgement requires worker and timestamp",
		)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delivery, exists := s.items[eventID]
	if !exists {
		return &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "audit outbox event not found",
		}
	}
	if delivery.Event.TenantID != principal.TenantID {
		return core.NewPermissionError(
			"audit outbox event belongs to another tenant",
		)
	}
	if delivery.LeaseOwner != workerID {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "audit outbox lease is owned by another worker",
		}
	}
	delivery.DeliveredAt = at
	delivery.LeaseOwner = ""
	delivery.LeaseUntil = time.Time{}
	delivery.LastError = ""
	s.items[eventID] = delivery
	return nil
}

func (s *MemoryOutbox) Fail(
	ctx context.Context,
	eventID string,
	failure DeliveryFailure,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDeliveryFailure(failure); err != nil {
		return err
	}
	principal, err := outboxPrincipal(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delivery, exists := s.items[eventID]
	if !exists {
		return &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "audit outbox event not found",
		}
	}
	if delivery.Event.TenantID != principal.TenantID {
		return core.NewPermissionError(
			"audit outbox event belongs to another tenant",
		)
	}
	if delivery.LeaseOwner != failure.WorkerID {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "audit outbox lease is owned by another worker",
		}
	}
	delivery.Attempts++
	delivery.LastAttemptAt = failure.At
	delivery.LastError = boundedError(failure.Error)
	delivery.LeaseOwner = ""
	delivery.LeaseUntil = time.Time{}
	delivery.NextAttemptAt = failure.NextAttemptAt
	if failure.DeadLetter {
		delivery.DeadLetteredAt = failure.At
		delivery.NextAttemptAt = time.Time{}
	}
	s.items[eventID] = delivery
	return nil
}

func (s *MemoryOutbox) DeadLetters(
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
			"audit dead-letter limit must be between 1 and 1000",
		)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Delivery, 0)
	for _, delivery := range s.items {
		if delivery.Event.TenantID == principal.TenantID &&
			!delivery.DeadLetteredAt.IsZero() {
			delivery.Event = cloneEvent(delivery.Event)
			output = append(output, delivery)
		}
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].DeadLetteredAt.Equal(output[j].DeadLetteredAt) {
			return output[i].Event.ID < output[j].Event.ID
		}
		return output[i].DeadLetteredAt.Before(output[j].DeadLetteredAt)
	})
	if len(output) > limit {
		output = output[:limit]
	}
	return output, nil
}

func (s *MemoryOutbox) Requeue(
	ctx context.Context,
	eventID string,
	at time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, err := outboxPrincipal(ctx)
	if err != nil {
		return err
	}
	if at.IsZero() {
		return core.NewConfigError(
			"audit dead-letter requeue requires a timestamp",
		)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delivery, exists := s.items[eventID]
	if !exists {
		return &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "audit outbox event not found",
		}
	}
	if delivery.Event.TenantID != principal.TenantID {
		return core.NewPermissionError(
			"audit outbox event belongs to another tenant",
		)
	}
	if delivery.DeadLetteredAt.IsZero() {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "audit outbox event is not dead-lettered",
		}
	}
	delivery.Attempts = 0
	delivery.DeadLetteredAt = time.Time{}
	delivery.NextAttemptAt = at
	delivery.LeaseOwner = ""
	delivery.LeaseUntil = time.Time{}
	s.items[eventID] = delivery
	return nil
}

var _ LeasedOutbox = (*MemoryOutbox)(nil)
