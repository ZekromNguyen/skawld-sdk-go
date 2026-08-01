package audit

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// A poll interrupted by shutdown (context cancellation) must not be recorded
// as an operational failure, or a graceful stop would flip readiness endpoints
// to failed and inflate the failure streak.
func TestWorkerCancellationDoesNotMarkHealthFailed(t *testing.T) {
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "audit-worker",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	outbox := NewMemoryOutbox()
	now := time.Unix(100, 0).UTC()
	worker, err := NewWorker(WorkerOptions{
		Outbox: outbox, Sink: &failingSink{}, WorkerID: "worker-1",
		BatchSize: 10, LeaseDuration: time.Minute,
		BaseBackoff: time.Second, MaxBackoff: time.Minute,
		MaxAttempts: 2, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("initial poll failed: %v", err)
	}
	if health := worker.Health(); !health.Ready ||
		health.ConsecutiveFailures != 0 {
		t.Fatalf("initial poll was not healthy: %+v", health)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := worker.RunOnce(canceled); err == nil {
		t.Fatal("expected a cancellation error from the canceled poll")
	}
	health := worker.Health()
	if !health.Ready || health.ConsecutiveFailures != 0 {
		t.Fatalf(
			"cancellation was recorded as an operational failure: %+v",
			health,
		)
	}
	if !health.LastAttemptAt.Equal(now) {
		t.Fatalf("cancelled poll did not record its attempt time: %+v", health)
	}
}
