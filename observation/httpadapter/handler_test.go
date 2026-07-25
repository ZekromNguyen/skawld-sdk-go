package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
)

func TestHandlerCapturesAuthenticatedSemanticBusinessEvent(t *testing.T) {
	fixture := newHTTPFixture(t)
	body := fixture.eventBody(t, "event-1")
	response := fixture.send(body, "tenant-a", "erp-service", fixture.now)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	demo, ok, err := fixture.store.Get(fixture.ctx, fixture.demonstrationID)
	if err != nil || !ok || len(demo.Trace.Events) != 1 {
		t.Fatalf("captured trace: ok=%t demo=%+v err=%v", ok, demo, err)
	}
	event := demo.Trace.Events[0]
	if event.ID != "event-1" ||
		event.Principal.TenantID != "tenant-a" ||
		event.Principal.ActorID != "erp-service" ||
		event.Source != observation.SourceAPI ||
		event.Trust != observation.TrustApplicationEvent ||
		event.Sensitivity != observation.SensitivityConfidential ||
		event.Application != "accounting" ||
		event.Action != "invoice.opened" {
		t.Fatalf("unexpected semantic event: %+v", event)
	}
}

func TestHandlerRejectsReplayUnknownFieldsBadSignatureAndOversize(t *testing.T) {
	fixture := newHTTPFixture(t)
	body := fixture.eventBody(t, "event-1")
	if response := fixture.send(body, "tenant-a", "erp-service", fixture.now); response.Code != http.StatusCreated {
		t.Fatalf("first capture status = %d", response.Code)
	}
	if response := fixture.send(body, "tenant-a", "erp-service", fixture.now); response.Code != http.StatusConflict {
		t.Fatalf("replay status = %d, want 409", response.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderTenantID, "tenant-a")
	request.Header.Set(HeaderActorID, "erp-service")
	request.Header.Set(HeaderTimestamp, fixture.now.Format(time.RFC3339Nano))
	request.Header.Set(HeaderSignature, "00")
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want 401", response.Code)
	}

	var untrusted map[string]interface{}
	if err := json.Unmarshal(fixture.eventBody(t, "event-2"), &untrusted); err != nil {
		t.Fatal(err)
	}
	untrusted["trust"] = "system_policy"
	unknownBody, _ := json.Marshal(untrusted)
	if response := fixture.send(
		unknownBody, "tenant-a", "erp-service", fixture.now,
	); response.Code != http.StatusBadRequest {
		t.Fatalf("client-supplied trust status = %d, want 400", response.Code)
	}

	smallHandler, err := New(Options{
		Sink: fixture.recorder, Authenticator: fixture.authenticator, MaxBodyBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	oversizeRequest := httptest.NewRequest(
		http.MethodPost, "/events", bytes.NewReader(bytes.Repeat([]byte("x"), 33)),
	)
	oversizeRequest.Header.Set("Content-Type", "application/json")
	oversizeResponse := httptest.NewRecorder()
	smallHandler.ServeHTTP(oversizeResponse, oversizeRequest)
	if oversizeResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status = %d, want 413", oversizeResponse.Code)
	}
}

func TestHandlerRejectsStaleAndCrossTenantRequests(t *testing.T) {
	fixture := newHTTPFixture(t)
	body := fixture.eventBody(t, "event-1")
	if response := fixture.send(
		body, "tenant-a", "erp-service", fixture.now.Add(-10*time.Minute),
	); response.Code != http.StatusUnauthorized {
		t.Fatalf("stale signature status = %d, want 401", response.Code)
	}
	if response := fixture.send(
		body, "tenant-b", "erp-service", fixture.now,
	); response.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant status = %d, want 403", response.Code)
	}
	if response := fixture.send(
		body, "tenant-a", "impersonated-service", fixture.now,
	); response.Code != http.StatusUnauthorized {
		t.Fatalf("unknown actor status = %d, want 401", response.Code)
	}
}

func TestHandlerMapsInvalidCorrectionToBadRequest(t *testing.T) {
	fixture := newHTTPFixture(t)
	event := BusinessEvent{
		EventID: "correction", DemonstrationID: fixture.demonstrationID,
		OccurredAt: fixture.now, Application: "accounting",
		Action: "invoice.corrected", CorrectionOf: "missing-event",
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.send(body, "tenant-a", "erp-service", fixture.now)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid correction status = %d, want 400", response.Code)
	}
}

func TestHandlerAtomicallyDeduplicatesConcurrentDeliveries(t *testing.T) {
	fixture := newHTTPFixture(t)
	body := fixture.eventBody(t, "event-concurrent")
	const deliveries = 16
	statuses := make(chan int, deliveries)
	var workers sync.WaitGroup
	for range deliveries {
		workers.Add(1)
		go func() {
			defer workers.Done()
			statuses <- fixture.send(
				body, "tenant-a", "erp-service", fixture.now,
			).Code
		}()
	}
	workers.Wait()
	close(statuses)
	created, conflicts := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent status %d", status)
		}
	}
	if created != 1 || conflicts != deliveries-1 {
		t.Fatalf("created=%d conflicts=%d", created, conflicts)
	}
}

func TestHMACAuthenticatorRejectsBodyTampering(t *testing.T) {
	now := time.Now().UTC()
	secret := []byte("tenant-secret")
	authenticator, err := NewHMACAuthenticator(HMACOptions{
		Secrets: NewStaticSecrets(map[Identity][]byte{
			{TenantID: "tenant-a", ActorID: "erp-service"}: secret,
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"event_id":"one"}`)
	request := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	timestamp := now.Format(time.RFC3339Nano)
	request.Header.Set(HeaderTenantID, "tenant-a")
	request.Header.Set(HeaderActorID, "erp-service")
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(
		HeaderSignature,
		Signature(secret, timestamp, "tenant-a", "erp-service", body),
	)
	if _, err := authenticator.Authenticate(
		context.Background(), request, []byte(`{"event_id":"two"}`),
	); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered body error = %v, want authentication failure", err)
	}
}

func TestHandlerCanMarkAuthenticatedPayloadAsUntrustedByPolicy(t *testing.T) {
	fixture := newHTTPFixture(t)
	handler, err := New(Options{
		Sink: fixture.recorder, Authenticator: fixture.authenticator,
		Name: "support-ticket-webhook", Trust: observation.TrustUntrustedContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = handler
	response := fixture.send(
		fixture.eventBody(t, "untrusted-event"),
		"tenant-a",
		"support-system",
		fixture.now,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	demo, ok, err := fixture.store.Get(fixture.ctx, fixture.demonstrationID)
	if err != nil || !ok || demo.Trace.Events[0].Trust != observation.TrustUntrustedContent {
		t.Fatalf("untrusted policy was not retained: demo=%+v err=%v", demo, err)
	}
	if handler.Metadata().Name != "support-ticket-webhook" {
		t.Fatalf("adapter metadata = %+v", handler.Metadata())
	}
}

type httpFixture struct {
	ctx             context.Context
	now             time.Time
	secret          []byte
	demonstrationID string
	store           *observation.MemoryStore
	recorder        *observation.Recorder
	authenticator   *HMACAuthenticator
	handler         *Handler
}

func newHTTPFixture(t *testing.T) httpFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	principal := core.Principal{TenantID: "tenant-a", ActorID: "trainer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	store := observation.NewMemoryStore()
	recorder, err := observation.NewRecorder(store)
	if err != nil {
		t.Fatal(err)
	}
	demonstration, err := recorder.Start(ctx, "invoice", principal, nil)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("tenant-a-secret")
	authenticator, err := NewHMACAuthenticator(HMACOptions{
		Secrets: NewStaticSecrets(map[Identity][]byte{
			{TenantID: "tenant-a", ActorID: "erp-service"}:    secret,
			{TenantID: "tenant-a", ActorID: "support-system"}: secret,
			{TenantID: "tenant-b", ActorID: "erp-service"}:    []byte("tenant-b-secret"),
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Options{Sink: recorder, Authenticator: authenticator})
	if err != nil {
		t.Fatal(err)
	}
	return httpFixture{
		ctx: ctx, now: now, secret: secret, demonstrationID: demonstration.ID,
		store: store, recorder: recorder, authenticator: authenticator, handler: handler,
	}
}

func (fixture httpFixture) eventBody(t *testing.T, eventID string) []byte {
	t.Helper()
	body, err := json.Marshal(BusinessEvent{
		EventID: eventID, DemonstrationID: fixture.demonstrationID,
		OccurredAt: fixture.now, Application: "accounting", Action: "invoice.opened",
		Intent: "inspect invoice",
		Entity: &observation.Entity{Type: "invoice", ID: "INV-1"},
		Input:  map[string]interface{}{"invoice_id": "INV-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (fixture httpFixture) send(
	body []byte,
	tenantID string,
	actorID string,
	timestamp time.Time,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	timestampText := timestamp.Format(time.RFC3339Nano)
	request.Header.Set(HeaderTenantID, tenantID)
	request.Header.Set(HeaderActorID, actorID)
	request.Header.Set(HeaderTimestamp, timestampText)
	secret := fixture.secret
	if tenantID == "tenant-b" {
		secret = []byte("tenant-b-secret")
	}
	request.Header.Set(
		HeaderSignature,
		Signature(secret, timestampText, tenantID, actorID, body),
	)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return response
}
