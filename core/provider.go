package core

import "context"

type SystemBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Cacheable bool   `json:"cacheable,omitempty"`
}

type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type ProviderRequest struct {
	Model           ModelID
	System          []SystemBlock
	Tools           []ToolSchema
	Messages        []Message
	MaxOutputTokens *int
	Temperature     *float64
	StopSequences   []string
	CachePrompt     bool
	CacheTTL        string
	Thinking        map[string]interface{}
	Effort          string
	MaxRetries      int
}

type ProviderStreamEvent struct {
	Type             string
	Model            ModelID
	Text             string
	Signature        string
	ID               string
	Name             string
	JSONDelta        string
	StopReason       StopReason
	Usage            Usage
	ProviderMetadata MessageProviderMetadata
}

type ProviderStreamResult struct {
	Event ProviderStreamEvent
	Err   error
}

type ProviderStream <-chan ProviderStreamResult

// Provider identifies a model provider and its context window.
//
// Provider values supplied through AgentOptions.Provider may be used by
// multiple sessions and parent/subagent runs concurrently, so shared provider
// implementations must be safe for concurrent Stream calls. If a provider has
// mutable per-run state and is not safe to share, supply a ProviderFactory so
// each subagent can receive its own provider instance.
type Provider interface {
	ID() string
	ContextWindow(model ModelID) int
}

// ProviderFactory creates a provider for a run owner such as a parent agent or
// subagent. Use it when a provider implementation is not safe to share across
// concurrent Stream calls.
type ProviderFactory interface {
	NewProvider() Provider
}

// StreamingProvider streams normalized provider events. Stream must return
// promptly when ctx is canceled and must not block indefinitely if the caller
// stops consuming the returned stream.
type StreamingProvider interface {
	Provider
	Stream(ctx context.Context, req ProviderRequest) ProviderStream
}

// LegacyStreamingProvider is the pre-single-stream contract kept for
// compatibility. New providers should implement StreamingProvider.
type LegacyStreamingProvider interface {
	Provider
	Stream(ctx context.Context, req ProviderRequest) (<-chan ProviderStreamEvent, <-chan error)
}

func SupportsStreamingProvider(provider Provider) bool {
	if provider == nil {
		return false
	}
	if _, ok := provider.(StreamingProvider); ok {
		return true
	}
	if _, ok := provider.(LegacyStreamingProvider); ok {
		return true
	}
	return false
}

func StreamProvider(ctx context.Context, provider Provider, req ProviderRequest) (ProviderStream, error) {
	if provider == nil {
		return nil, NewConfigError("provider is nil")
	}
	if p, ok := provider.(StreamingProvider); ok {
		return p.Stream(ctx, req), nil
	}
	if p, ok := provider.(LegacyStreamingProvider); ok {
		streamCtx, cancel := context.WithCancel(ctx)
		events, errs := p.Stream(streamCtx, req)
		return adaptLegacyProviderStream(streamCtx, cancel, events, errs), nil
	}
	return nil, NewConfigError("provider does not implement Stream")
}

func adaptLegacyProviderStream(ctx context.Context, cancel context.CancelFunc, events <-chan ProviderStreamEvent, errs <-chan error) ProviderStream {
	out := make(chan ProviderStreamResult)
	go func() {
		defer close(out)
		defer cancel()
		for events != nil || errs != nil {
			select {
			case ev, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if !sendProviderStreamResult(ctx, out, ProviderStreamResult{Event: ev}) {
					return
				}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					_ = sendProviderStreamResult(ctx, out, ProviderStreamResult{Err: err})
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func sendProviderStreamResult(ctx context.Context, out chan<- ProviderStreamResult, result ProviderStreamResult) bool {
	select {
	case out <- result:
		return true
	default:
	}
	select {
	case out <- result:
		return true
	case <-ctx.Done():
		return false
	}
}
