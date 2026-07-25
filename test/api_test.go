package skawld_test

import (
	"context"
	"testing"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/config"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
	"github.com/ZekromNguyen/skawld-sdk-go/tools/mcp"
)

type apiProvider struct{}

func (p apiProvider) ID() string { return "api" }
func (p apiProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p apiProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "ok"}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn}
	}()
	return out, errs
}

func TestPublicAPISmoke(t *testing.T) {
	agent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider:     apiProvider{},
		Model:        "fake-model",
		Tools:        tools.DefaultTools(),
		SessionStore: sessions.NewInMemoryStore(),
		Permissions:  skawld.PermissionOptions{Mode: skawld.PermissionModeDefault},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), skawld.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range session.Run(context.Background(), "hello", skawld.RunOptions{}) {
		if ev.Type == skawld.EventResult && ev.Subtype == "success" {
			return
		}
	}
	t.Fatal("expected successful result")
}

func TestPublicAPIConfigAdapter(t *testing.T) {
	opts := skawld.AgentOptionsFromConfig(configOptionsForAPI(apiProvider{}))
	if opts.Provider == nil || opts.Tools == nil || opts.SessionStore == nil {
		t.Fatalf("expected populated options: %+v", opts)
	}
	if len(opts.MCPServers) != 1 || opts.MCPServers[0].Name != "srv" {
		t.Fatalf("expected MCP config passthrough: %+v", opts.MCPServers)
	}
}

func configOptionsForAPI(provider skawld.Provider) config.AgentOptions {
	return config.AgentOptions{
		Provider:       provider,
		Model:          "fake-model",
		PermissionMode: skawld.PermissionModeDefault,
		MCPServers:     []mcp.ServerConfig{{Name: "srv", HTTP: &mcp.HTTPServerConfig{URL: "https://example.test/mcp"}}},
	}
}
