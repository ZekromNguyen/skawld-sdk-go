// Package httpadapter receives authenticated business events over HTTP and
// converts them into semantic observation events.
package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
)

const (
	defaultMaxBodyBytes int64 = 1 << 20
	maxBodyBytes        int64 = 16 << 20
)

type Options struct {
	Sink          observation.Sink
	Authenticator Authenticator
	MaxBodyBytes  int64
	Name          string
	Trust         observation.Trust
	Sensitivity   observation.Sensitivity
}

type Handler struct {
	sink          observation.Sink
	authenticator Authenticator
	maxBodyBytes  int64
	metadata      observation.AdapterMetadata
	trust         observation.Trust
	sensitivity   observation.Sensitivity
}

type BusinessEvent struct {
	EventID         string                 `json:"event_id"`
	DemonstrationID string                 `json:"demonstration_id"`
	OccurredAt      time.Time              `json:"occurred_at"`
	Application     string                 `json:"application"`
	Action          string                 `json:"action"`
	Intent          string                 `json:"intent,omitempty"`
	Entity          *observation.Entity    `json:"entity,omitempty"`
	Input           map[string]interface{} `json:"input,omitempty"`
	Output          map[string]interface{} `json:"output,omitempty"`
	Context         map[string]interface{} `json:"context,omitempty"`
	Decision        map[string]interface{} `json:"decision,omitempty"`
	Result          map[string]interface{} `json:"result,omitempty"`
	Error           string                 `json:"error,omitempty"`
	CorrectionOf    string                 `json:"correction_of,omitempty"`
	ApprovalID      string                 `json:"approval_id,omitempty"`
}

func New(options Options) (*Handler, error) {
	if options.Sink == nil {
		return nil, core.NewConfigError("HTTP observation adapter requires a sink")
	}
	if options.Authenticator == nil {
		return nil, core.NewConfigError(
			"HTTP observation adapter requires an authenticator",
		)
	}
	if options.MaxBodyBytes == 0 {
		options.MaxBodyBytes = defaultMaxBodyBytes
	}
	if options.MaxBodyBytes < 1 || options.MaxBodyBytes > maxBodyBytes {
		return nil, core.NewConfigError(
			"HTTP observation body limit must be between 1 byte and 16 MiB",
		)
	}
	if options.Name == "" {
		options.Name = "http.business_event"
	}
	if options.Trust == "" {
		options.Trust = observation.TrustApplicationEvent
	}
	if options.Trust != observation.TrustApplicationEvent &&
		options.Trust != observation.TrustUntrustedContent {
		return nil, core.NewConfigError(
			"HTTP observation trust must be application_event or untrusted_content",
		)
	}
	if options.Sensitivity == "" {
		options.Sensitivity = observation.SensitivityConfidential
	}
	switch options.Sensitivity {
	case observation.SensitivityPublic, observation.SensitivityInternal,
		observation.SensitivityConfidential, observation.SensitivityRestricted:
	default:
		return nil, core.NewConfigError(
			"HTTP observation sensitivity is invalid",
		)
	}
	metadata := observation.AdapterMetadata{Name: options.Name, Source: observation.SourceAPI}
	if err := metadata.Validate(); err != nil {
		return nil, err
	}
	return &Handler{
		sink: options.Sink, authenticator: options.Authenticator,
		maxBodyBytes: options.MaxBodyBytes, metadata: metadata,
		trust: options.Trust, sensitivity: options.Sensitivity,
	}, nil
}

func (handler *Handler) Metadata() observation.AdapterMetadata {
	return handler.metadata
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, handler.maxBodyBytes+1))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_body")
		return
	}
	if int64(len(body)) > handler.maxBodyBytes {
		writeError(response, http.StatusRequestEntityTooLarge, "body_too_large")
		return
	}
	principal, err := handler.authenticator.Authenticate(request.Context(), request, body)
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			response.Header().Set("WWW-Authenticate", `Skawld-HMAC realm="observations"`)
			writeError(response, http.StatusUnauthorized, "authentication_failed")
			return
		}
		if request.Context().Err() != nil {
			writeError(response, http.StatusRequestTimeout, "request_canceled")
			return
		}
		writeError(response, http.StatusInternalServerError, "authentication_unavailable")
		return
	}
	if principal.TenantID == "" || principal.ActorID == "" {
		writeError(response, http.StatusUnauthorized, "authentication_failed")
		return
	}
	var businessEvent BusinessEvent
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&businessEvent); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_event")
		return
	}
	if err := requireJSONEOF(decoder); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_event")
		return
	}
	if err := businessEvent.Validate(); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_event")
		return
	}
	event := observation.Event{
		ID: businessEvent.EventID, Principal: principal,
		Timestamp: businessEvent.OccurredAt,
		Source:    observation.SourceAPI, Trust: handler.trust,
		Sensitivity: handler.sensitivity,
		Application: businessEvent.Application, Action: businessEvent.Action,
		Intent: businessEvent.Intent, Entity: businessEvent.Entity,
		Input: businessEvent.Input, Output: businessEvent.Output,
		Context: businessEvent.Context, Decision: businessEvent.Decision,
		Result: businessEvent.Result, Error: businessEvent.Error,
		CorrectionOf: businessEvent.CorrectionOf, ApprovalID: businessEvent.ApprovalID,
	}
	ctx := core.WithPrincipal(request.Context(), principal)
	captured, err := handler.sink.Capture(ctx, businessEvent.DemonstrationID, event)
	if err != nil {
		writeCaptureError(response, err)
		return
	}
	response.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(response).Encode(map[string]string{
		"demonstration_id": businessEvent.DemonstrationID,
		"event_id":         captured.ID,
	})
}

func (event BusinessEvent) Validate() error {
	required := []string{
		event.EventID, event.DemonstrationID, event.Application, event.Action,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return core.NewConfigError(
				"business event identity, application, and action are required and limited to 256 bytes",
			)
		}
	}
	if event.OccurredAt.IsZero() {
		return core.NewConfigError("business event occurred_at is required")
	}
	if len(event.Intent) > 2048 || len(event.Error) > 4096 ||
		len(event.CorrectionOf) > 256 || len(event.ApprovalID) > 256 {
		return core.NewConfigError("business event text field exceeds its limit")
	}
	if event.Entity != nil &&
		(strings.TrimSpace(event.Entity.Type) == "" ||
			len(event.Entity.Type) > 256 ||
			len(event.Entity.ID) > 256) {
		return core.NewConfigError("business event entity is invalid")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeCaptureError(response http.ResponseWriter, err error) {
	var sdkError *core.SkawldError
	if errors.As(err, &sdkError) {
		switch sdkError.Kind {
		case core.ErrorPermissionDenied:
			writeError(response, http.StatusForbidden, "capture_forbidden")
		case core.ErrorNotFound:
			writeError(response, http.StatusNotFound, "demonstration_not_found")
		case core.ErrorConflict:
			writeError(response, http.StatusConflict, "event_conflict")
		case core.ErrorConfig, core.ErrorValidation:
			writeError(response, http.StatusBadRequest, "invalid_event")
		default:
			writeError(response, http.StatusInternalServerError, "capture_failed")
		}
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(response, http.StatusRequestTimeout, "request_canceled")
		return
	}
	writeError(response, http.StatusInternalServerError, "capture_failed")
}

func writeError(response http.ResponseWriter, status int, code string) {
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": code})
}

var (
	_ http.Handler        = (*Handler)(nil)
	_ observation.Adapter = (*Handler)(nil)
)
