package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/observation/httpadapter"
)

func main() {
	principal := core.Principal{TenantID: "demo-company", ActorID: "trainer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store := observation.NewMemoryStore()
	recorder, err := observation.NewRecorder(store)
	if err != nil {
		log.Fatal(err)
	}
	demonstration, err := recorder.Start(
		ctx, "invoice-reconciliation", principal,
		map[string]interface{}{"environment": "fixture"},
	)
	if err != nil {
		log.Fatal(err)
	}

	secret := []byte("local-example-secret")
	authenticator, err := httpadapter.NewHMACAuthenticator(httpadapter.HMACOptions{
		Secrets: httpadapter.NewStaticSecrets(map[httpadapter.Identity][]byte{
			{TenantID: principal.TenantID, ActorID: "accounting-system"}: secret,
		}),
	})
	if err != nil {
		log.Fatal(err)
	}
	handler, err := httpadapter.New(httpadapter.Options{
		Sink: recorder, Authenticator: authenticator,
	})
	if err != nil {
		log.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	occurredAt := time.Now().UTC()
	body, err := json.Marshal(httpadapter.BusinessEvent{
		EventID: "erp-event-1001", DemonstrationID: demonstration.ID,
		OccurredAt: occurredAt, Application: "accounting",
		Action: "invoice.opened", Intent: "inspect invoice before reconciliation",
		Entity: &observation.Entity{Type: "invoice", ID: "INV-1001"},
		Input:  map[string]interface{}{"invoice_id": "INV-1001"},
	})
	if err != nil {
		log.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, server.URL, bytes.NewReader(body),
	)
	if err != nil {
		log.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(httpadapter.HeaderTenantID, principal.TenantID)
	request.Header.Set(httpadapter.HeaderActorID, "accounting-system")
	request.Header.Set(httpadapter.HeaderTimestamp, timestamp)
	request.Header.Set(
		httpadapter.HeaderSignature,
		httpadapter.Signature(
			secret, timestamp, principal.TenantID, "accounting-system", body,
		),
	)
	response, err := server.Client().Do(request)
	if err != nil {
		log.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusCreated {
		log.Fatalf("capture status: %s", response.Status)
	}
	captured, ok, err := store.Get(ctx, demonstration.ID)
	if err != nil || !ok {
		log.Fatalf("load demonstration: ok=%t err=%v", ok, err)
	}
	fmt.Printf(
		"demonstration=%s events=%d source=%s trust=%s\n",
		captured.ID,
		len(captured.Trace.Events),
		captured.Trace.Events[0].Source,
		captured.Trace.Events[0].Trust,
	)
}
