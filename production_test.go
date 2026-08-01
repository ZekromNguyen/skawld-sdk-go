package skawld

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/permissions"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

type durableTestSessionStore struct {
	*sessions.InMemoryStore
}

func (durableTestSessionStore) Durable() bool   { return true }
func (durableTestSessionStore) Protected() bool { return true }

type durableUnprotectedTestSessionStore struct {
	*sessions.InMemoryStore
}

func (durableUnprotectedTestSessionStore) Durable() bool { return true }

type durableTestOutbox struct {
	*audit.MemoryOutbox
}

func (durableTestOutbox) Durable() bool   { return true }
func (durableTestOutbox) Protected() bool { return true }

type fixedAgentPolicy struct {
	decision policy.Decision
}

func (p fixedAgentPolicy) Evaluate(
	context.Context,
	policy.Action,
) (policy.Decision, error) {
	return p.decision, nil
}

func (fixedAgentPolicy) EnforcesCapabilities() bool { return true }

type productionTestTool struct {
	calls int
}

func (*productionTestTool) Name() string { return "customer.lookup" }
func (*productionTestTool) Description() string {
	return "look up a customer"
}
func (*productionTestTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (*productionTestTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (*productionTestTool) ParallelSafe() bool    { return false }
func (*productionTestTool) Validate(
	raw map[string]interface{},
) (map[string]interface{}, error) {
	return raw, nil
}
func (t *productionTestTool) Execute(
	map[string]interface{},
	core.ToolContext,
) (core.ToolResult, error) {
	t.calls++
	return core.ToolResult{Content: "invalid"}, nil
}
func (*productionTestTool) Summarize(map[string]interface{}) string {
	return "lookup customer"
}
func (*productionTestTool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskLow, SideEffect: core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
		Timeout:     time.Second,
		Permissions: []string{
			"customer.read",
		},
		OutputSchema: map[string]interface{}{
			"type":     "object",
			"required": []interface{}{"id"},
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string"},
			},
		},
	}
}

type productionTestProvider struct {
	calls            int
	maxOutputTokens  int
	maxOutputHistory []int
}

func (*productionTestProvider) ID() string { return "production-test" }
func (*productionTestProvider) ContextWindow(core.ModelID) int {
	return 10000
}
func (p *productionTestProvider) Stream(
	ctx context.Context,
	req core.ProviderRequest,
) core.ProviderStream {
	out := make(chan core.ProviderStreamResult, 8)
	p.calls++
	if req.MaxOutputTokens != nil {
		p.maxOutputTokens = *req.MaxOutputTokens
		p.maxOutputHistory = append(
			p.maxOutputHistory, *req.MaxOutputTokens,
		)
	}
	call := p.calls
	go func() {
		defer close(out)
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "message_start", Model: req.Model,
		}}
		if call == 1 {
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
				Type: "tool_use_start", ID: "call-1",
				Name: "customer.lookup",
			}}
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
				Type: "tool_use_input_delta", ID: "call-1",
				JSONDelta: `{}`,
			}}
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
				Type: "tool_use_end", ID: "call-1",
			}}
			out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
				Type: "message_end", StopReason: core.StopToolUse,
				Usage: core.Usage{InputTokens: 10, OutputTokens: 2},
			}}
			return
		}
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "text_delta", Text: "done",
		}}
		out <- core.ProviderStreamResult{Event: core.ProviderStreamEvent{
			Type: "message_end", StopReason: core.StopEndTurn,
			Usage: core.Usage{InputTokens: 10, OutputTokens: 1},
		}}
	}()
	return out
}

type productionProtocolTestProvider struct {
	events []core.ProviderStreamEvent
	calls  int
}

func (*productionProtocolTestProvider) ID() string { return "protocol-test" }
func (*productionProtocolTestProvider) ContextWindow(core.ModelID) int {
	return 10000
}
func (p *productionProtocolTestProvider) Stream(
	ctx context.Context,
	req core.ProviderRequest,
) core.ProviderStream {
	out := make(chan core.ProviderStreamResult, len(p.events))
	p.calls++
	go func() {
		defer close(out)
		for _, event := range p.events {
			select {
			case out <- core.ProviderStreamResult{Event: event}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func productionLimitsForTest() RuntimeLimits {
	return RuntimeLimits{
		MaxRunDuration: time.Second, MaxToolCalls: 4,
		MaxToolResultBytes: 1024, MaxProviderResponseBytes: 4096,
		MaxProviderEvents: 100, MaxOutputTokensPerTurn: 100,
		MaxSessionBytes: 16384, MaxTotalTokens: 1000,
	}
}

func TestProductionAgentRejectsNonDurableDependencies(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	_, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools:        registry,
		SessionStore: sessions.NewInMemoryStore(),
		Principal: core.Principal{
			TenantID: "tenant-a", ActorID: "actor-a",
		},
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: audit.NewMemoryOutbox(),
			Limits:      productionLimitsForTest(),
		},
	})
	if err == nil {
		t.Fatal("expected non-durable dependency rejection")
	}
}

func TestProductionAgentRejectsUnprotectedDurableSessionStore(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	_, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableUnprotectedTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: core.Principal{
			TenantID: "tenant-a", ActorID: "actor-a",
		},
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err == nil {
		t.Fatal("expected unprotected session-store rejection")
	}
}

func TestProductionAgentValidatesToolOutput(t *testing.T) {
	tool := &productionTestTool{}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawValidationFailure := false
	for event := range session.Run(ctx, "lookup", RunOptions{}) {
		if event.Type == core.EventToolCallEnd && event.IsError {
			sawValidationFailure = true
		}
	}
	if tool.calls != 1 || !sawValidationFailure {
		t.Fatalf(
			"expected one validated tool call, calls=%d failure=%v",
			tool.calls, sawValidationFailure,
		)
	}
}

func TestProductionSessionRequiresAuthenticatedContext(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	if _, err := agent.Session(
		context.Background(), SessionOptions{},
	); err == nil {
		t.Fatal("expected unauthenticated production session rejection")
	}
}

func TestProductionRunRevalidatesSessionIdentity(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
		Roles: []string{"support"},
	}
	provider := &productionTestProvider{}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: provider, Model: "test", Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	session, err := agent.Session(
		core.WithPrincipal(context.Background(), principal),
		SessionOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	sawPermissionError := false
	for event := range session.Run(
		context.Background(), "lookup", RunOptions{},
	) {
		if event.Type == core.EventError &&
			event.Error != nil &&
			event.Error.Name == "PermissionError" {
			sawPermissionError = true
		}
	}
	if !sawPermissionError || provider.calls != 0 {
		t.Fatalf(
			"unauthenticated run was not rejected: error=%v calls=%d",
			sawPermissionError, provider.calls,
		)
	}
}

func TestProductionHardPolicyDenialCannotBeBypassed(t *testing.T) {
	tool := &productionTestTool{}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Permissions: PermissionOptions{
			CanUseTool: func(
				context.Context,
				permissions.CanUseToolRequest,
			) (permissions.CanUseToolResponse, error) {
				return permissions.CanUseToolResponse{
					Behavior: "allow",
				}, nil
			},
		},
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{
					Kind: policy.Deny, Reason: "capability missing",
				},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(ctx, "lookup", RunOptions{}) {
	}
	if tool.calls != 0 {
		t.Fatalf("hard-denied tool executed %d times", tool.calls)
	}
}

func TestProductionAgentClampsPerTurnOutputTokens(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	provider := &productionTestProvider{}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: provider, Model: "test", Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	requested := 10_000
	for range session.Run(
		ctx, "lookup", RunOptions{MaxOutputTokens: &requested},
	) {
	}
	if provider.maxOutputTokens !=
		productionLimitsForTest().MaxOutputTokensPerTurn {
		t.Fatalf(
			"provider output limit was not clamped: %d",
			provider.maxOutputTokens,
		)
	}
}

func TestProductionAgentRejectsMalformedProviderToolProtocol(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	provider := &productionProtocolTestProvider{
		events: []core.ProviderStreamEvent{
			{
				Type: "tool_use_input_delta", ID: "missing-start",
				JSONDelta: `{}`,
			},
		},
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: provider, Model: "test", Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawProviderError := false
	for event := range session.Run(ctx, "lookup", RunOptions{}) {
		if event.Type == core.EventError && event.Error != nil &&
			event.Error.Name == string(core.ErrorProvider) {
			sawProviderError = true
		}
	}
	if !sawProviderError {
		t.Fatal("malformed provider tool protocol was not rejected")
	}
}

type mutableProductionTool struct {
	productionTestTool
	sideEffect core.SideEffectKind
}

func (t *mutableProductionTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := t.productionTestTool.ToolDescriptor()
	descriptor.SideEffect = t.sideEffect
	return descriptor
}

func TestProductionAgentRevalidatesToolContractsBeforeRun(t *testing.T) {
	tool := &mutableProductionTool{sideEffect: core.SideEffectNone}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	provider := &productionTestProvider{}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: provider, Model: "test", Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	tool.sideEffect = core.SideEffectUnknown
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(ctx, "lookup", RunOptions{}) {
	}
	if provider.calls != 0 || tool.calls != 0 {
		t.Fatalf(
			"changed tool contract reached execution: provider=%d tool=%d",
			provider.calls, tool.calls,
		)
	}
}

type mutableInputProductionTool struct {
	productionTestTool
	mutate        bool
	mutateInPlace bool
}

func (t *mutableInputProductionTool) Validate(
	raw map[string]interface{},
) (map[string]interface{}, error) {
	if t.mutateInPlace {
		raw["changed"] = true
		return raw, nil
	}
	if t.mutate {
		return map[string]interface{}{"changed": true}, nil
	}
	return raw, nil
}

type mutatingInputPolicy struct {
	tool *mutableInputProductionTool
}

func (p mutatingInputPolicy) Evaluate(
	context.Context,
	policy.Action,
) (policy.Decision, error) {
	p.tool.mutate = true
	return policy.Decision{Kind: policy.Allow}, nil
}

func (mutatingInputPolicy) EnforcesCapabilities() bool { return true }

func TestProductionAgentRejectsInputMutationAfterPolicy(t *testing.T) {
	tool := &mutableInputProductionTool{}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: mutatingInputPolicy{tool: tool},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(ctx, "lookup", RunOptions{}) {
	}
	if tool.calls != 0 {
		t.Fatalf("mutated production input executed %d times", tool.calls)
	}
}

type inPlaceMutatingInputPolicy struct {
	tool *mutableInputProductionTool
}

func (p inPlaceMutatingInputPolicy) Evaluate(
	context.Context,
	policy.Action,
) (policy.Decision, error) {
	p.tool.mutateInPlace = true
	return policy.Decision{Kind: policy.Allow}, nil
}

func (inPlaceMutatingInputPolicy) EnforcesCapabilities() bool { return true }

func TestProductionAgentRejectsInPlaceInputMutationAfterPolicy(
	t *testing.T,
) {
	tool := &mutableInputProductionTool{}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: inPlaceMutatingInputPolicy{tool: tool},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(ctx, "lookup", RunOptions{}) {
	}
	if tool.calls != 0 {
		t.Fatalf("in-place mutated production input executed %d times", tool.calls)
	}
}

type capturingProductionTool struct {
	productionTestTool
	input map[string]interface{}
}

func (t *capturingProductionTool) Execute(
	input map[string]interface{},
	toolCtx core.ToolContext,
) (core.ToolResult, error) {
	t.input = input
	return t.productionTestTool.Execute(input, toolCtx)
}

func TestProductionPermissionCallbackCannotMutateAuthorizedInputInPlace(
	t *testing.T,
) {
	tool := &capturingProductionTool{}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Permissions: PermissionOptions{
			CanUseTool: func(
				_ context.Context,
				request permissions.CanUseToolRequest,
			) (permissions.CanUseToolResponse, error) {
				request.Input["rewritten"] = true
				return permissions.CanUseToolResponse{
					Behavior: "allow",
				}, nil
			},
		},
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{
					Kind: policy.RequireApproval,
				},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(ctx, "lookup", RunOptions{}) {
	}
	if tool.calls != 1 {
		t.Fatalf("production tool calls = %d, want 1", tool.calls)
	}
	if _, mutated := tool.input["rewritten"]; mutated {
		t.Fatalf("permission callback mutation reached execution: %#v", tool.input)
	}
}

type invalidInputSchemaTool struct {
	productionTestTool
	schema map[string]interface{}
}

func (t *invalidInputSchemaTool) InputSchema() map[string]interface{} {
	return t.schema
}

func TestProductionAgentRequiresValidTrustedInputSchema(t *testing.T) {
	for name, schema := range map[string]map[string]interface{}{
		"missing": nil,
		"invalid": {
			"type": "not-a-json-schema-type",
		},
	} {
		t.Run(name, func(t *testing.T) {
			registry := tools.NewRegistry()
			if err := registry.Register(
				&invalidInputSchemaTool{schema: schema},
			); err != nil {
				t.Fatal(err)
			}
			_, err := NewProductionAgent(AgentOptions{
				Provider: &productionTestProvider{}, Model: "test",
				Tools: registry,
				SessionStore: durableTestSessionStore{
					InMemoryStore: sessions.NewInMemoryStore(),
				},
				Principal: core.Principal{
					TenantID: "tenant-a", ActorID: "actor-a",
				},
				Production: &ProductionOptions{
					Policy: fixedAgentPolicy{
						decision: policy.Decision{Kind: policy.Allow},
					},
					AuditOutbox: durableTestOutbox{
						MemoryOutbox: audit.NewMemoryOutbox(),
					},
					Limits: productionLimitsForTest(),
				},
			})
			if err == nil {
				t.Fatal("expected invalid production input schema rejection")
			}
		})
	}
}

func TestProductionPermissionCannotRewriteAuthorizedToolInput(t *testing.T) {
	tool := &productionTestTool{}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: &productionTestProvider{}, Model: "test",
		Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Permissions: PermissionOptions{
			CanUseTool: func(
				context.Context,
				permissions.CanUseToolRequest,
			) (permissions.CanUseToolResponse, error) {
				return permissions.CanUseToolResponse{
					Behavior: "allow",
					UpdatedInput: map[string]interface{}{
						"rewritten": true,
					},
				}, nil
			},
		},
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{
					Kind: policy.RequireApproval,
				},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(ctx, "lookup", RunOptions{}) {
	}
	if tool.calls != 0 {
		t.Fatalf("rewritten production input executed %d times", tool.calls)
	}
}

func TestProductionProviderUsageMustBeNonNegative(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	provider := &productionProtocolTestProvider{
		events: []core.ProviderStreamEvent{
			{Type: "message_start", Model: "test"},
			{
				Type: "message_end", StopReason: core.StopEndTurn,
				Usage: core.Usage{OutputTokens: -1},
			},
		},
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	agent, err := NewProductionAgent(AgentOptions{
		Provider: provider, Model: "test", Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: productionLimitsForTest(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawProviderError := false
	sawAssistant := false
	for event := range session.Run(ctx, "lookup", RunOptions{}) {
		if event.Type == core.EventError && event.Error != nil &&
			event.Error.Name == string(core.ErrorProvider) {
			sawProviderError = true
		}
		if event.Type == core.EventAssistant {
			sawAssistant = true
		}
	}
	if !sawProviderError || sawAssistant {
		t.Fatalf(
			"invalid usage was not rejected before persistence: error=%v assistant=%v",
			sawProviderError, sawAssistant,
		)
	}
}

func TestProductionAgentClampsOutputToRemainingRunBudget(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&productionTestTool{}); err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	provider := &productionTestProvider{}
	limits := productionLimitsForTest()
	limits.MaxOutputTokensPerTurn = 15
	limits.MaxTotalTokens = 15
	agent, err := NewProductionAgent(AgentOptions{
		Provider: provider, Model: "test", Tools: registry,
		SessionStore: durableTestSessionStore{
			InMemoryStore: sessions.NewInMemoryStore(),
		},
		Principal: principal,
		Production: &ProductionOptions{
			Policy: fixedAgentPolicy{
				decision: policy.Decision{Kind: policy.Allow},
			},
			AuditOutbox: durableTestOutbox{
				MemoryOutbox: audit.NewMemoryOutbox(),
			},
			Limits: limits,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ctx := core.WithPrincipal(context.Background(), principal)
	session, err := agent.Session(ctx, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(ctx, "lookup", RunOptions{}) {
	}
	if len(provider.maxOutputHistory) != 2 ||
		provider.maxOutputHistory[0] != 15 ||
		provider.maxOutputHistory[1] != 3 {
		t.Fatalf(
			"provider output budget did not track remaining tokens: %v",
			provider.maxOutputHistory,
		)
	}
}
