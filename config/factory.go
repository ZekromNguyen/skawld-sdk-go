package config

import (
	"context"
	"fmt"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/providers"
)

// ProviderFactory creates a core.Provider from a config File. Callers can
// inject a custom factory to avoid importing concrete providers, or use
// DefaultProviderFactory for the built-in Anthropic and OpenAI adapters.
type ProviderFactory interface {
	NewProvider(ctx context.Context, cfg File) (core.Provider, error)
}

// DefaultProviderFactory is the built-in ProviderFactory that wires the
// config File to the concrete providers (Anthropic, OpenAI Chat, OpenAI
// Responses).
type DefaultProviderFactory struct{}

// NewProvider implements ProviderFactory.
func (DefaultProviderFactory) NewProvider(ctx context.Context, cfg File) (core.Provider, error) {
	switch cfg.Provider {
	case "openai-responses":
		return providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{
			APIKey:         cfg.OpenAI.APIKey,
			BaseURL:        cfg.OpenAI.BaseURL,
			DefaultHeaders: cfg.OpenAI.DefaultHeaders,
		}), nil
	case "openai-chat":
		return providers.NewOpenAIChatCompletionsProvider(providers.OpenAIOptions{
			APIKey:         cfg.OpenAI.APIKey,
			BaseURL:        cfg.OpenAI.BaseURL,
			DefaultHeaders: cfg.OpenAI.DefaultHeaders,
		}), nil
	case "anthropic":
		return providers.NewAnthropicProvider(providers.AnthropicOptions{
			APIKey:         cfg.Anthropic.APIKey,
			BaseURL:        cfg.Anthropic.BaseURL,
			DefaultHeaders: cfg.Anthropic.DefaultHeaders,
		}), nil
	default:
		return nil, core.NewConfigError(fmt.Sprintf("unsupported provider %q", cfg.Provider))
	}
}
