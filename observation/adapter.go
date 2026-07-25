package observation

import (
	"context"
	"fmt"
	"strings"
)

// AdapterMetadata identifies an observation ingress without coupling the core
// package to its transport or lifecycle.
type AdapterMetadata struct {
	Name   string `json:"name"`
	Source Source `json:"source"`
}

func (metadata AdapterMetadata) Validate() error {
	if strings.TrimSpace(metadata.Name) == "" {
		return fmt.Errorf("observation adapter name is required")
	}
	if !validSource(metadata.Source) {
		return fmt.Errorf("observation adapter source %q is invalid", metadata.Source)
	}
	return nil
}

// Adapter is the common identity contract for browser, desktop, API, CLI,
// database, email, and other observation ingress implementations. Each
// adapter owns its transport lifecycle and emits semantic events through Sink.
type Adapter interface {
	Metadata() AdapterMetadata
}

// Sink is the adapter-facing semantic capture boundary. Recorder implements
// Sink and remains responsible for demonstration state and tenant validation.
type Sink interface {
	Capture(context.Context, string, Event) (Event, error)
}

type SinkFunc func(context.Context, string, Event) (Event, error)

func (capture SinkFunc) Capture(
	ctx context.Context,
	demonstrationID string,
	event Event,
) (Event, error) {
	return capture(ctx, demonstrationID, event)
}

var _ Sink = (*Recorder)(nil)
