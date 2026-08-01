package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type failingSink struct {
	fail  bool
	calls int
}

func (s *failingSink) Append(context.Context, Event) error {
	s.calls++
	if s.fail {
		return errors.New("sink unavailable")
	}
	return nil
}

func TestDispatcherKeepsFailedDeliveryDurableAndFlushes(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "worker"}
	ctx := core.WithPrincipal(context.Background(), principal)
	outbox := NewMemoryOutbox()
	sink := &failingSink{fail: true}
	dispatcher, err := NewDispatcher(outbox, sink)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		ID: "event-1", Type: EventToolCompleted, Timestamp: time.Now().UTC(),
		TenantID: principal.TenantID, ActorID: principal.ActorID,
	}
	if err := dispatcher.Append(ctx, event); err != nil {
		t.Fatalf("durably queued delivery returned an error: %v", err)
	}
	pending, err := outbox.Pending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Attempts != 1 ||
		pending[0].LastError == "" {
		t.Fatalf("unexpected pending delivery: %+v", pending)
	}
	sink.fail = false
	if err := dispatcher.Flush(ctx, 10); err != nil {
		t.Fatal(err)
	}
	pending, err = outbox.Pending(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("delivery was not marked complete: %+v err=%v", pending, err)
	}
}

func TestWorkerLeasesBacksOffAndDeadLetters(t *testing.T) {
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "audit-worker",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	outbox := NewMemoryOutbox()
	now := time.Unix(100, 0).UTC()
	outbox.now = func() time.Time { return now }
	event := Event{
		ID: "event-dead", Type: EventToolCompleted, Timestamp: now,
		TenantID: principal.TenantID, ActorID: principal.ActorID,
	}
	if err := outbox.Enqueue(ctx, event); err != nil {
		t.Fatal(err)
	}
	sink := &failingSink{fail: true}
	worker, err := NewWorker(WorkerOptions{
		Outbox: outbox, Sink: sink, WorkerID: "worker-1",
		BatchSize: 1, LeaseDuration: time.Minute,
		BaseBackoff: time.Second, MaxBackoff: time.Minute,
		MaxAttempts: 2, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOnce(ctx)
	if err == nil || result.Failed != 1 ||
		result.DeadLettered != 0 {
		t.Fatalf("first attempt result=%+v err=%v", result, err)
	}
	health := worker.Health()
	if health.Ready || health.ConsecutiveFailures != 1 ||
		health.LastResult.Failed != 1 ||
		health.WorkerID != "worker-1" {
		t.Fatalf("failed delivery health was not recorded: %+v", health)
	}
	result, err = worker.RunOnce(ctx)
	if err != nil || result.Claimed != 0 {
		t.Fatalf("event ignored backoff: result=%+v err=%v", result, err)
	}
	health = worker.Health()
	if !health.Ready || health.ConsecutiveFailures != 0 ||
		health.LastSuccessAt.IsZero() ||
		!health.Healthy(now, time.Second) ||
		health.Healthy(now.Add(2*time.Second), time.Second) {
		t.Fatalf("successful poll did not restore readiness: %+v", health)
	}
	now = now.Add(time.Second)
	result, err = worker.RunOnce(ctx)
	if err == nil || result.DeadLettered != 1 {
		t.Fatalf("dead letter result=%+v err=%v", result, err)
	}
	delivery := outbox.items[event.ID]
	if delivery.Attempts != 2 || delivery.DeadLetteredAt.IsZero() ||
		delivery.LeaseOwner != "" {
		t.Fatalf("unexpected dead letter state: %+v", delivery)
	}
	dead, err := outbox.DeadLetters(ctx, 10)
	if err != nil || len(dead) != 1 {
		t.Fatalf("dead letters=%+v err=%v", dead, err)
	}
	if err := outbox.Requeue(
		ctx, event.ID, now.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	if len(outbox.items) != 1 ||
		!outbox.items[event.ID].DeadLetteredAt.IsZero() ||
		outbox.items[event.ID].Attempts != 0 {
		t.Fatalf("dead letter was not requeued: %+v", outbox.items[event.ID])
	}
}

func TestOutboxLeaseExcludesAndRejectsStaleWorker(t *testing.T) {
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "audit-worker",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	outbox := NewMemoryOutbox()
	now := time.Unix(100, 0).UTC()
	if err := outbox.Enqueue(ctx, Event{
		ID: "event-lease", Type: EventToolCalled, Timestamp: now,
		TenantID: principal.TenantID,
	}); err != nil {
		t.Fatal(err)
	}
	first, err := outbox.Claim(ctx, LeaseRequest{
		WorkerID: "worker-1", Limit: 1,
		LeaseDuration: time.Minute, Now: now,
	})
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	second, err := outbox.Claim(ctx, LeaseRequest{
		WorkerID: "worker-2", Limit: 1,
		LeaseDuration: time.Minute, Now: now,
	})
	if err != nil || len(second) != 0 {
		t.Fatalf("live lease was duplicated: %+v err=%v", second, err)
	}
	now = now.Add(2 * time.Minute)
	second, err = outbox.Claim(ctx, LeaseRequest{
		WorkerID: "worker-2", Limit: 1,
		LeaseDuration: time.Minute, Now: now,
	})
	if err != nil || len(second) != 1 {
		t.Fatalf("expired lease was not reclaimed: %+v err=%v", second, err)
	}
	if err := outbox.Acknowledge(
		ctx, "event-lease", "worker-1", now,
	); !errors.Is(err, &core.SkawldError{
		Kind: core.ErrorConflict,
	}) {
		t.Fatalf("stale worker acknowledgement error=%v", err)
	}
	if err := outbox.Acknowledge(
		ctx, "event-lease", "worker-2", now,
	); err != nil {
		t.Fatal(err)
	}
}
