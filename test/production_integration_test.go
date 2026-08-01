package skawld_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
	sessionssqlite "github.com/ZekromNguyen/skawld-sdk-go/sessions/sqlite"
	sdkstorage "github.com/ZekromNguyen/skawld-sdk-go/storage"
	workflowsqlite "github.com/ZekromNguyen/skawld-sdk-go/storage/sqlite"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

type productionIntegrationProvider struct {
	turn int
}

func (*productionIntegrationProvider) ID() string {
	return "production-integration"
}
func (*productionIntegrationProvider) ContextWindow(core.ModelID) int {
	return 10000
}
func (p *productionIntegrationProvider) Stream(
	ctx context.Context,
	req core.ProviderRequest,
) core.ProviderStream {
	out := make(chan core.ProviderStreamResult, 8)
	p.turn++
	turn := p.turn
	go func() {
		defer close(out)
		events := []core.ProviderStreamEvent{{
			Type: "message_start", Model: req.Model,
		}}
		if turn == 1 {
			events = append(events,
				core.ProviderStreamEvent{
					Type: "tool_use_start", ID: "call-1",
					Name: "customer.lookup",
				},
				core.ProviderStreamEvent{
					Type: "tool_use_input_delta", ID: "call-1",
					JSONDelta: `{}`,
				},
				core.ProviderStreamEvent{
					Type: "tool_use_end", ID: "call-1",
				},
				core.ProviderStreamEvent{
					Type: "message_end", StopReason: core.StopToolUse,
					Usage: core.Usage{InputTokens: 2, OutputTokens: 1},
				},
			)
		} else {
			events = append(events,
				core.ProviderStreamEvent{
					Type: "text_delta", Text: "done",
				},
				core.ProviderStreamEvent{
					Type: "message_end", StopReason: core.StopEndTurn,
					Usage: core.Usage{InputTokens: 2, OutputTokens: 1},
				},
			)
		}
		for _, event := range events {
			select {
			case out <- core.ProviderStreamResult{Event: event}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

type productionIntegrationTool struct{}

func (productionIntegrationTool) Name() string { return "customer.lookup" }
func (productionIntegrationTool) Description() string {
	return "look up a customer"
}
func (productionIntegrationTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (productionIntegrationTool) Scope() core.ToolScope {
	return core.ToolScopeRead
}
func (productionIntegrationTool) ParallelSafe() bool { return false }
func (productionIntegrationTool) Validate(
	input map[string]interface{},
) (map[string]interface{}, error) {
	return input, nil
}
func (productionIntegrationTool) Execute(
	map[string]interface{},
	core.ToolContext,
) (core.ToolResult, error) {
	return core.ToolResult{
		Content: map[string]interface{}{"id": "customer-1"},
	}, nil
}
func (productionIntegrationTool) Summarize(
	map[string]interface{},
) string {
	return "look up customer"
}
func (productionIntegrationTool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskLow, SideEffect: core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
		Timeout:     time.Second,
		Permissions: []string{"customer.read"},
		OutputSchema: map[string]interface{}{
			"type":     "object",
			"required": []interface{}{"id"},
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string"},
			},
		},
	}
}

func TestProductionAgentUsesProtectedSQLitePersistence(t *testing.T) {
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
		Roles: []string{"support"},
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	keys, err := sdkstorage.NewStaticKeyProvider(
		map[string]sdkstorage.EncryptionKey{
			principal.TenantID: {
				ID: "key-a", Bytes: bytes.Repeat([]byte{9}, 32),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := sdkstorage.NewAESGCMProtector(keys)
	if err != nil {
		t.Fatal(err)
	}
	rawSessions, err := sessionssqlite.Open(
		filepath.Join(t.TempDir(), "sessions.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	protectedSessions, err := sessions.NewProtectedStore(
		rawSessions, protector,
	)
	if err != nil {
		t.Fatal(err)
	}
	workflowStore, err := workflowsqlite.OpenWithOptions(
		ctx, filepath.Join(t.TempDir(), "workflow.db"),
		workflowsqlite.Options{
			Protector: protector, RequireProtection: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer workflowStore.Close()
	authorization, err := policy.NewRolePolicy(
		policy.RolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"support": {"customer.read"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	if err := registry.Register(productionIntegrationTool{}); err != nil {
		t.Fatal(err)
	}
	agent, err := skawld.NewProductionAgent(skawld.AgentOptions{
		Provider: &productionIntegrationProvider{}, Model: "test",
		Tools: registry, SessionStore: protectedSessions,
		Principal: principal,
		Production: &skawld.ProductionOptions{
			Policy:      authorization,
			AuditOutbox: workflowStore.AuditOutbox(),
			Limits: skawld.RuntimeLimits{
				MaxRunDuration: time.Second, MaxToolCalls: 4,
				MaxToolResultBytes:       1024,
				MaxProviderResponseBytes: 4096,
				MaxProviderEvents:        100,
				MaxOutputTokensPerTurn:   100,
				MaxSessionBytes:          16384,
				MaxTotalTokens:           1000,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(ctx, skawld.SessionOptions{
		Meta: map[string]interface{}{"case": "sensitive-case"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var success bool
	for event := range session.Run(
		ctx, "lookup customer", skawld.RunOptions{},
	) {
		if event.Type == core.EventResult && event.Subtype == "success" {
			success = true
		}
	}
	if !success {
		t.Fatal("production agent did not complete through protected stores")
	}
	if err := agent.Close(); err != nil {
		t.Fatal(err)
	}
}
