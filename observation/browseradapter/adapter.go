// Package browseradapter converts browser-extension or instrumentation events
// into semantic observations. It deliberately accepts accessible element
// identity and business values, not coordinates, selectors, or executable
// scripts.
package browseradapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
)

const (
	defaultMaxEventBytes = 256 << 10
	maxEventBytes        = 2 << 20
)

type Action string

const (
	ActionNavigate Action = "navigate"
	ActionActivate Action = "activate"
	ActionInput    Action = "input"
	ActionSelect   Action = "select"
	ActionSubmit   Action = "submit"
	ActionExtract  Action = "extract"
	ActionDownload Action = "download"
	ActionUpload   Action = "upload"
)

type Page struct {
	Origin string `json:"origin"`
	Path   string `json:"path,omitempty"`
	Title  string `json:"title,omitempty"`
}

// Element identifies a target through application/accessibility semantics.
// StableID is an application-owned identifier such as a data-testid, not a CSS
// selector or DOM path.
type Element struct {
	Role     string `json:"role"`
	Name     string `json:"name,omitempty"`
	Label    string `json:"label,omitempty"`
	StableID string `json:"stable_id,omitempty"`
}

type Event struct {
	EventID         string                 `json:"event_id"`
	DemonstrationID string                 `json:"demonstration_id"`
	OccurredAt      time.Time              `json:"occurred_at"`
	Application     string                 `json:"application"`
	Action          Action                 `json:"action"`
	Intent          string                 `json:"intent,omitempty"`
	Page            Page                   `json:"page"`
	Element         *Element               `json:"element,omitempty"`
	Input           map[string]interface{} `json:"input,omitempty"`
	Output          map[string]interface{} `json:"output,omitempty"`
	Context         map[string]interface{} `json:"context,omitempty"`
	Result          map[string]interface{} `json:"result,omitempty"`
	Error           string                 `json:"error,omitempty"`
	CorrectionOf    string                 `json:"correction_of,omitempty"`
	ApprovalID      string                 `json:"approval_id,omitempty"`
}

type Options struct {
	Sink          observation.Sink
	Name          string
	Trust         observation.Trust
	Sensitivity   observation.Sensitivity
	MaxEventBytes int
}

type Adapter struct {
	sink          observation.Sink
	metadata      observation.AdapterMetadata
	trust         observation.Trust
	sensitivity   observation.Sensitivity
	maxEventBytes int
}

func New(options Options) (*Adapter, error) {
	if options.Sink == nil {
		return nil, core.NewConfigError(
			"browser observation adapter requires a sink",
		)
	}
	if options.Name == "" {
		options.Name = "browser.semantic_event"
	}
	if !validLabel(options.Name, 128) {
		return nil, core.NewConfigError(
			"browser observation adapter name is invalid",
		)
	}
	if options.Trust == "" {
		options.Trust = observation.TrustUntrustedContent
	}
	if options.Trust != observation.TrustUntrustedContent &&
		options.Trust != observation.TrustApplicationEvent {
		return nil, core.NewConfigError(
			"browser observation trust must be untrusted_content or application_event",
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
			"browser observation sensitivity is invalid",
		)
	}
	if options.MaxEventBytes == 0 {
		options.MaxEventBytes = defaultMaxEventBytes
	}
	if options.MaxEventBytes < 1 || options.MaxEventBytes > maxEventBytes {
		return nil, core.NewConfigError(
			"browser observation event limit must be between 1 byte and 2 MiB",
		)
	}
	return &Adapter{
		sink: options.Sink,
		metadata: observation.AdapterMetadata{
			Name: options.Name, Source: observation.SourceBrowser,
		},
		trust: options.Trust, sensitivity: options.Sensitivity,
		maxEventBytes: options.MaxEventBytes,
	}, nil
}

func (a *Adapter) Metadata() observation.AdapterMetadata {
	return a.metadata
}

func (a *Adapter) Capture(
	ctx context.Context,
	event Event,
) (observation.Event, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || !principal.Authenticated() {
		return observation.Event{}, core.NewPermissionError(
			"browser observation requires authenticated tenant and actor identities",
		)
	}
	if err := event.validate(); err != nil {
		return observation.Event{}, err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return observation.Event{}, &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "browser observation is not JSON serializable", Cause: err,
		}
	}
	if len(raw) > a.maxEventBytes {
		return observation.Event{}, &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "browser observation exceeds configured size limit",
		}
	}
	contextValues := cloneMap(event.Context)
	if contextValues == nil {
		contextValues = make(map[string]interface{})
	}
	browserContext := map[string]interface{}{
		"page": map[string]interface{}{
			"origin": event.Page.Origin,
			"path":   event.Page.Path,
			"title":  event.Page.Title,
		},
	}
	var entity *observation.Entity
	if event.Element != nil {
		browserContext["element"] = map[string]interface{}{
			"role":      event.Element.Role,
			"name":      event.Element.Name,
			"label":     event.Element.Label,
			"stable_id": event.Element.StableID,
		}
		entity = &observation.Entity{
			Type: event.Element.Role, ID: event.Element.StableID,
		}
	}
	contextValues["browser"] = browserContext
	captured := observation.Event{
		ID: event.EventID, Principal: principal,
		Timestamp: event.OccurredAt, Source: observation.SourceBrowser,
		Trust: a.trust, Sensitivity: a.sensitivity,
		Application: event.Application,
		Action:      string(event.Action), Intent: event.Intent, Entity: entity,
		Input: cloneMap(event.Input), Output: cloneMap(event.Output),
		Context: contextValues, Result: cloneMap(event.Result),
		Error: event.Error, CorrectionOf: event.CorrectionOf,
		ApprovalID: event.ApprovalID,
	}
	return a.sink.Capture(ctx, event.DemonstrationID, captured)
}

func (event Event) validate() error {
	if !validLabel(event.EventID, 256) ||
		!validLabel(event.DemonstrationID, 256) ||
		!validLabel(event.Application, 128) ||
		event.OccurredAt.IsZero() {
		return core.NewConfigError(
			"browser observation requires valid event, demonstration, application, and timestamp fields",
		)
	}
	switch event.Action {
	case ActionNavigate, ActionActivate, ActionInput, ActionSelect,
		ActionSubmit, ActionExtract, ActionDownload, ActionUpload:
	default:
		return core.NewConfigError(
			fmt.Sprintf("browser observation action %q is invalid", event.Action),
		)
	}
	if event.Action != ActionNavigate && event.Element == nil {
		return core.NewConfigError(
			"browser interaction observation requires a semantic element",
		)
	}
	if err := event.Page.validate(); err != nil {
		return err
	}
	if event.Element != nil {
		if err := event.Element.validate(); err != nil {
			return err
		}
	}
	if len(event.Intent) > 1024 || len(event.Error) > 4096 ||
		strings.ContainsAny(event.Intent+event.Error, "\x00") {
		return core.NewConfigError(
			"browser observation text exceeds its safe bounds",
		)
	}
	for name, values := range map[string]map[string]interface{}{
		"input": event.Input, "output": event.Output,
		"context": event.Context, "result": event.Result,
	} {
		if err := rejectReplayData(values, name, 0); err != nil {
			return err
		}
	}
	return nil
}

func (page Page) validate() error {
	parsed, err := url.Parse(page.Origin)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		parsed.Path != "" && parsed.Path != "/" {
		return core.NewConfigError(
			"browser page origin must be an HTTP(S) origin without path, credentials, query, or fragment",
		)
	}
	if page.Path != "" &&
		(!strings.HasPrefix(page.Path, "/") ||
			len(page.Path) > 2048 ||
			strings.ContainsAny(page.Path, "\r\n\x00")) {
		return core.NewConfigError("browser page path is invalid")
	}
	if len(page.Title) > 512 ||
		strings.ContainsAny(page.Title, "\r\n\x00") {
		return core.NewConfigError("browser page title is invalid")
	}
	return nil
}

func (element Element) validate() error {
	if !validLabel(element.Role, 64) {
		return core.NewConfigError(
			"browser semantic element requires a valid accessibility role",
		)
	}
	for _, value := range []string{
		element.Name, element.Label, element.StableID,
	} {
		if len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
			return core.NewConfigError(
				"browser semantic element identity is invalid",
			)
		}
	}
	return nil
}

func rejectReplayData(
	values map[string]interface{},
	path string,
	depth int,
) error {
	if depth > 24 {
		return core.NewConfigError(
			"browser observation data exceeds maximum nesting",
		)
	}
	for key, value := range values {
		normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
		switch normalized {
		case "x", "y", "coordinates", "mouse_position", "selector",
			"css_selector", "xpath", "script", "javascript":
			return core.NewConfigError(
				fmt.Sprintf(
					"browser observation %s.%s contains forbidden replay data",
					path, key,
				),
			)
		}
		if err := rejectReplayValue(
			value, path+"."+key, depth+1,
		); err != nil {
			return err
		}
	}
	return nil
}

func rejectReplayValue(value interface{}, path string, depth int) error {
	if depth > 24 {
		return core.NewConfigError(
			"browser observation data exceeds maximum nesting",
		)
	}
	switch nested := value.(type) {
	case map[string]interface{}:
		return rejectReplayData(nested, path, depth)
	case []interface{}:
		for index, item := range nested {
			if err := rejectReplayValue(
				item, fmt.Sprintf("%s.%d", path, index), depth+1,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validLabel(value string, max int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max ||
		strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	return true
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	raw, _ := json.Marshal(input)
	var output map[string]interface{}
	_ = json.Unmarshal(raw, &output)
	return output
}

var _ observation.Adapter = (*Adapter)(nil)
