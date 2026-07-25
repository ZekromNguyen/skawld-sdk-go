package observation

import (
	"context"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestRecorderAppliesTrustedRedactionPolicy(t *testing.T) {
	redactor, err := NewRedactor(RedactorOptions{
		Rules: map[string]RedactionAction{
			"initial_context.access_token": RedactDrop,
			"input.card.number":            RedactMask,
			"output.credentials.*":         RedactDrop,
			"final_result.secret":          RedactDrop,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	recorder, err := NewRecorderWithOptions(RecorderOptions{
		Store: store, Sanitizer: redactor,
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{TenantID: "tenant-a", ActorID: "operator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demo, err := recorder.Start(ctx, "payment", principal, map[string]interface{}{
		"access_token": "secret", "region": "apac",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := demo.Trace.InitialContext["access_token"]; exists {
		t.Fatal("initial context secret was retained")
	}
	captured, err := recorder.Capture(ctx, demo.ID, Event{
		Source: SourceAPI, Trust: TrustApplicationEvent, Action: "pay",
		Input: map[string]interface{}{
			"card": map[string]interface{}{"number": "4111111111111111"},
		},
		Output: map[string]interface{}{
			"credentials": map[string]interface{}{
				"token": "secret", "refresh": "secret",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	card := captured.Input["card"].(map[string]interface{})
	if card["number"] != "[REDACTED]" {
		t.Fatalf("card was not masked: %+v", card)
	}
	if len(captured.Output["credentials"].(map[string]interface{})) != 0 {
		t.Fatalf("credentials were retained: %+v", captured.Output)
	}
	if captured.Sensitivity != SensitivityInternal {
		t.Fatalf("unexpected default sensitivity %q", captured.Sensitivity)
	}
	completed, err := recorder.Complete(ctx, demo.ID, map[string]interface{}{
		"secret": "hidden", "status": "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := completed.Trace.FinalResult["secret"]; exists {
		t.Fatal("final result secret was retained")
	}
}
