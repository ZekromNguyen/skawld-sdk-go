package observation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestAdapterMetadataAndSinkContract(t *testing.T) {
	metadata := AdapterMetadata{Name: "business-http", Source: SourceAPI}
	if err := metadata.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AdapterMetadata{Source: Source("unknown")}).Validate(); err == nil {
		t.Fatal("invalid adapter metadata was accepted")
	}
	called := false
	var sink Sink = SinkFunc(func(
		context.Context,
		string,
		Event,
	) (Event, error) {
		called = true
		return Event{ID: "captured"}, nil
	})
	if _, err := sink.Capture(context.Background(), "demo", Event{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("sink function was not called")
	}
}

func TestRecorderRejectsDuplicateAdapterEventIDs(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store := NewMemoryStore()
	recorder, err := NewRecorder(store)
	if err != nil {
		t.Fatal(err)
	}
	demo, err := recorder.Start(ctx, "invoice", principal, nil)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		ID: "business-event-1", Timestamp: time.Now().UTC(),
		Source: SourceAPI, Trust: TrustApplicationEvent,
		Action: "invoice.opened",
	}
	if _, err := recorder.Capture(ctx, demo.ID, event); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Capture(ctx, demo.ID, event); !errors.Is(
		err, &core.SkawldError{Kind: core.ErrorConflict},
	) {
		t.Fatalf("duplicate capture error = %v, want conflict", err)
	}
	loaded, ok, err := store.Get(ctx, demo.ID)
	if err != nil || !ok || len(loaded.Trace.Events) != 1 {
		t.Fatalf("duplicate event reached trace: ok=%t trace=%+v err=%v", ok, loaded.Trace, err)
	}
}
