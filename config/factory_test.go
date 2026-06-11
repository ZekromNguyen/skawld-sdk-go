package config

import (
	"context"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

// fakeProvider is a minimal core.Provider for testing config factory wiring.
type fakeProvider struct {
	id string
}

func (p fakeProvider) ID() string                           { return p.id }
func (p fakeProvider) ContextWindow(model core.ModelID) int { return 128000 }

type fakeProviderFactory struct {
	provider core.Provider
}

func (f fakeProviderFactory) NewProvider(ctx context.Context, cfg File) (core.Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return f.provider, nil
}

func TestAgentOptionsWithFakeFactory(t *testing.T) {
	fake := fakeProvider{id: "test-provider"}
	factory := fakeProviderFactory{provider: fake}

	cfg := File{
		Provider: "openai-chat",
		Model:    "gpt-4o",
	}

	opts, err := cfg.AgentOptionsWithFactory(context.Background(), factory)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Provider == nil {
		t.Fatal("expected non-nil provider")
	}
	if opts.Provider.ID() != "test-provider" {
		t.Fatalf("expected 'test-provider', got %q", opts.Provider.ID())
	}
}

func TestAgentOptionsWithFactoryValidation(t *testing.T) {
	fake := fakeProvider{id: "test"}
	factory := fakeProviderFactory{provider: fake}

	cfg := File{
		Provider: "",
		Model:    "",
	}

	_, err := cfg.AgentOptionsWithFactory(context.Background(), factory)
	if err == nil {
		t.Fatal("expected validation error for empty config")
	}
}

func TestAgentOptionsWithFactoryUnknownProvider(t *testing.T) {
	// The default factory rejects unknown providers
	factory := DefaultProviderFactory{}

	cfg := File{
		Provider: "unknown-vendor",
		Model:    "some-model",
	}

	_, err := cfg.AgentOptionsWithFactory(context.Background(), factory)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestAgentOptionsWrapsFactoryError(t *testing.T) {
	factory := fakeProviderFactory{provider: nil} // returns nil provider

	cfg := File{
		Provider: "openai-chat",
		Model:    "gpt-4o",
	}

	opts, err := cfg.AgentOptionsWithFactory(context.Background(), factory)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if opts.Provider != nil {
		t.Fatal("expected nil provider from factory")
	}
}

func TestProviderFactoryInterface(t *testing.T) {
	// Verify DefaultProviderFactory implements ProviderFactory
	var _ ProviderFactory = DefaultProviderFactory{}
}
