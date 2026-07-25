package observation

import (
	"context"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestRecorderBuildsTenantBoundSemanticTrace(t *testing.T) {
	store := NewMemoryStore()
	recorder, err := NewRecorder(store)
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{TenantID: "tenant-a", ActorID: "accountant-1"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demo, err := recorder.Start(ctx, "invoice-reconciliation", principal, map[string]interface{}{"source": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := recorder.Capture(ctx, demo.ID, Event{
		Source: SourceAPI, Trust: TrustApplicationEvent, Application: "invoice_demo",
		Action: "open_invoice", Intent: "inspect invoice",
		Entity: &Entity{Type: "invoice", ID: "INV-1"},
		Input:  map[string]interface{}{"invoice_id": "INV-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.SessionID != demo.Trace.SessionID || event.SchemaVersion != SchemaVersion {
		t.Fatalf("recorder did not normalize event: %+v", event)
	}
	completed, err := recorder.Complete(ctx, demo.ID, map[string]interface{}{"matched": true})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != DemonstrationCompleted || len(completed.Trace.Events) != 1 {
		t.Fatalf("unexpected demonstration: %+v", completed)
	}
	completed.Trace.Events[0].Action = "mutated"
	reloaded, _, err := store.Get(ctx, demo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Trace.Events[0].Action != "open_invoice" {
		t.Fatal("store leaked mutable trace state")
	}
}

func TestRecorderRejectsCrossTenantEvent(t *testing.T) {
	store := NewMemoryStore()
	recorder, _ := NewRecorder(store)
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a"})
	demo, err := recorder.Start(ctx, "workflow", core.Principal{TenantID: "tenant-a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = recorder.Capture(ctx, demo.ID, Event{
		Principal: core.Principal{TenantID: "tenant-b"},
		Source:    SourceAPI, Trust: TrustApplicationEvent, Action: "unsafe",
	})
	if err == nil {
		t.Fatal("expected cross-tenant observation to be rejected")
	}
}
