package skawld

import (
	"context"
	"errors"
	"log/slog"

	"github.com/skawld/skawld-sdk-go/core"
)

func (a *Agent) Observe(ctx context.Context, observation core.Observation) {
	a.observe(ctx, observation)
}

func (a *Agent) observe(ctx context.Context, observation core.Observation) {
	if a == nil {
		return
	}
	normalizeObservation(&observation)
	if a.opts.Observer != nil {
		a.opts.Observer.Observe(ctx, observation)
	}
	if a.opts.Logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("type", string(observation.Type)),
		slog.String("operation", observation.Operation),
		slog.Int64("duration_ms", observation.DurationMS),
	}
	if observation.SessionID != "" {
		attrs = append(attrs, slog.String("session_id", observation.SessionID))
	}
	if observation.RunID != "" {
		attrs = append(attrs, slog.String("run_id", observation.RunID))
	}
	if observation.ProviderID != "" {
		attrs = append(attrs, slog.String("provider_id", observation.ProviderID))
	}
	if observation.ToolName != "" {
		attrs = append(attrs, slog.String("tool_name", observation.ToolName))
	}
	if observation.Attempt > 0 {
		attrs = append(attrs, slog.Int("attempt", observation.Attempt))
	}
	if observation.Retryable {
		attrs = append(attrs, slog.Bool("retryable", true))
	}
	if observation.ErrorKind != "" {
		attrs = append(attrs, slog.String("error_kind", string(observation.ErrorKind)))
	}
	if observation.Error != nil {
		attrs = append(attrs, slog.String("error", observation.Error.Error()))
		a.opts.Logger.LogAttrs(ctx, slog.LevelError, observationMessage(observation), attrs...)
		return
	}
	a.opts.Logger.LogAttrs(ctx, slog.LevelInfo, observationMessage(observation), attrs...)
}

func normalizeObservation(observation *core.Observation) {
	if observation == nil || observation.Error == nil {
		return
	}
	var skerr *core.SkawldError
	if errors.As(observation.Error, &skerr) {
		observation.ErrorKind = skerr.Kind
		observation.Retryable = skerr.Retryable
	}
}

func observationMessage(observation core.Observation) string {
	switch observation.Type {
	case core.ObservationProviderAttempt:
		return "provider attempt"
	case core.ObservationToolExecution:
		return "tool execution"
	case core.ObservationPermissionCallback:
		return "permission callback"
	case core.ObservationCompaction:
		return "compaction"
	case core.ObservationMCPCall:
		return "mcp call"
	case core.ObservationStoreOperation:
		return "store operation"
	default:
		return "sdk observation"
	}
}
