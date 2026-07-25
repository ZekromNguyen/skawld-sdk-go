package browseradapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
)

type captureSink struct {
	demonstrationID string
	event           observation.Event
}

func (s *captureSink) Capture(
	_ context.Context,
	demonstrationID string,
	event observation.Event,
) (observation.Event, error) {
	s.demonstrationID = demonstrationID
	s.event = event
	return event, nil
}

func TestAdapterCapturesAccessibleSemanticAction(t *testing.T) {
	sink := &captureSink{}
	adapter, err := New(Options{Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	captured, err := adapter.Capture(ctx, Event{
		EventID: "event-1", DemonstrationID: "demo-1",
		OccurredAt: time.Now().UTC(), Application: "accounting.web",
		Action: ActionSubmit, Intent: "submit reviewed invoice",
		Page: Page{
			Origin: "https://accounting.example", Path: "/invoices/INV-1",
		},
		Element: &Element{
			Role: "button", Name: "Submit", StableID: "invoice-submit",
		},
		Input: map[string]interface{}{"invoice_id": "INV-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Source != observation.SourceBrowser ||
		captured.Trust != observation.TrustUntrustedContent ||
		captured.Sensitivity != observation.SensitivityConfidential ||
		captured.Entity == nil || captured.Entity.Type != "button" ||
		sink.demonstrationID != "demo-1" {
		t.Fatalf("unexpected semantic observation: %+v", captured)
	}
	browser, ok := captured.Context["browser"].(map[string]interface{})
	if !ok || browser["element"] == nil {
		t.Fatalf("browser context missing semantic target: %+v", captured.Context)
	}
}

func TestAdapterRejectsCoordinateSelectorAndScriptReplayData(t *testing.T) {
	adapter, err := New(Options{Sink: &captureSink{}})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	for _, input := range []map[string]interface{}{
		{"x": 421, "y": 203},
		{"selector": "#submit"},
		{"script": "document.querySelector('#submit').click()"},
	} {
		_, err := adapter.Capture(ctx, Event{
			EventID: "event-1", DemonstrationID: "demo-1",
			OccurredAt: time.Now().UTC(), Application: "accounting.web",
			Action: ActionActivate,
			Page:   Page{Origin: "https://accounting.example"},
			Element: &Element{
				Role: "button", Name: "Submit",
			},
			Input: input,
		})
		if err == nil || !strings.Contains(err.Error(), "forbidden replay data") {
			t.Fatalf("input %+v error = %v", input, err)
		}
	}
}
