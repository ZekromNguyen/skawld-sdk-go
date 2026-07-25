package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

type scriptedAgentProvider struct {
	mu   sync.Mutex
	turn int
}

func (p *scriptedAgentProvider) ID() string                     { return "scripted-evaluation" }
func (p *scriptedAgentProvider) ContextWindow(core.ModelID) int { return 100000 }

func (p *scriptedAgentProvider) Stream(ctx context.Context, request core.ProviderRequest) core.ProviderStream {
	out := make(chan core.ProviderStreamResult, 8)
	p.mu.Lock()
	p.turn++
	turn := p.turn
	p.mu.Unlock()
	go func() {
		defer close(out)
		send := func(event core.ProviderStreamEvent) bool {
			select {
			case out <- core.ProviderStreamResult{Event: event}:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !send(core.ProviderStreamEvent{Type: "message_start", Model: request.Model}) {
			return
		}
		if turn == 1 {
			if !send(core.ProviderStreamEvent{Type: "tool_use_start", ID: "lookup-1", Name: "FixtureLookup"}) ||
				!send(core.ProviderStreamEvent{
					Type: "tool_use_input_delta", ID: "lookup-1", JSONDelta: `{"invoice_id":"INV-1"}`,
				}) ||
				!send(core.ProviderStreamEvent{Type: "tool_use_end", ID: "lookup-1"}) {
				return
			}
			send(core.ProviderStreamEvent{
				Type: "message_end", StopReason: core.StopToolUse,
				Usage: core.Usage{InputTokens: 10, OutputTokens: 2},
			})
			return
		}
		if !send(core.ProviderStreamEvent{Type: "text_delta", Text: "SECRET-FINAL"}) {
			return
		}
		send(core.ProviderStreamEvent{
			Type: "message_end", StopReason: core.StopEndTurn,
			Usage: core.Usage{InputTokens: 5, OutputTokens: 1},
		})
	}()
	return out
}

type fixtureLookupTool struct{}

func (fixtureLookupTool) Name() string        { return "FixtureLookup" }
func (fixtureLookupTool) Description() string { return "looks up a fixture invoice" }
func (fixtureLookupTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{
			"invoice_id": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"invoice_id"},
	}
}
func (fixtureLookupTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (fixtureLookupTool) ParallelSafe() bool    { return true }
func (fixtureLookupTool) Validate(input map[string]interface{}) (map[string]interface{}, error) {
	if _, ok := input["invoice_id"].(string); !ok {
		return nil, core.NewConfigError("invoice_id is required")
	}
	return input, nil
}
func (fixtureLookupTool) Execute(map[string]interface{}, core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{Content: map[string]interface{}{"status": "found"}}, nil
}
func (fixtureLookupTool) Summarize(map[string]interface{}) string { return "look up invoice" }
func (fixtureLookupTool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskLow, SideEffect: core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
	}
}

func TestAgentRunnerEvaluatesRealSDKRuntimeWithoutPersistingContent(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "evaluator"}
	ctx := core.WithPrincipal(context.Background(), principal)
	agentStore := NewMemoryAgentStore()
	workdir := t.TempDir()
	executor := SDKAgentExecutor{Factory: func(_ context.Context, scenario AgentScenario) (*skawld.Agent, error) {
		registry := tools.NewRegistry()
		if err := registry.Register(fixtureLookupTool{}); err != nil {
			return nil, err
		}
		return skawld.NewAgent(skawld.AgentOptions{
			Provider: &scriptedAgentProvider{}, Model: "fixture-model", Tools: registry,
			Principal: scenario.Principal, CWD: workdir, MaxTurns: 4,
			DisableSkills: true, DisableSubagents: true,
			ProviderRetry: &skawld.ProviderRetryPolicy{MaxRetries: 0},
		})
	}}
	runner, err := NewAgentRunner(AgentRunnerOptions{
		Executor: executor, Store: agentStore, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalText := "SECRET-FINAL"
	maxCalls := 2
	suite := AgentSuite{
		Name: "support-agent-release",
		Scenarios: []AgentScenario{
			{
				ID: "ticket-1", Prompt: "SECRET-PROMPT-1",
				Expected: AgentExpectedOutcome{
					StopReason: core.StopEndTurn, FinalText: &finalText, MaxLLMCalls: &maxCalls,
					ToolCalls: []ExpectedToolCall{{
						Name: "FixtureLookup", Arguments: map[string]interface{}{"invoice_id": "INV-1"},
					}},
				},
			},
			{
				ID: "ticket-2", Prompt: "SECRET-PROMPT-2",
				Expected: AgentExpectedOutcome{
					StopReason: core.StopEndTurn, FinalText: &finalText, MaxLLMCalls: &maxCalls,
					ToolCalls: []ExpectedToolCall{{
						Name: "FixtureLookup", Arguments: map[string]interface{}{"invoice_id": "INV-1"},
					}},
				},
			},
		},
		Gates: []Gate{
			{Metric: MetricTaskSuccessRate, Operator: GateAtLeast, Value: 1},
			{Metric: MetricToolSelectionAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricParameterAccuracy, Operator: GateAtLeast, Value: 1},
			{Metric: MetricUnsafeActionRate, Operator: GateAtMost, Value: 0},
			{Metric: MetricAverageLLMCalls, Operator: GateAtMost, Value: 2},
		},
	}
	report, err := runner.Run(ctx, suite)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gates.Passed || report.Metrics.TaskSuccessRate.Value != 1 ||
		report.Metrics.AverageLLMCalls != 2 || report.Metrics.AverageInputTokens != 15 ||
		report.Metrics.AverageOutputTokens != 3 {
		t.Fatalf("unexpected agent evaluation: %+v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET-PROMPT", "SECRET-FINAL", "INV-1"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("agent report leaked scenario content %q: %s", secret, raw)
		}
	}
	if stored, ok, err := agentStore.GetAgentReport(ctx, report.ID); err != nil || !ok ||
		stored.SuiteName != suite.Name {
		t.Fatalf("stored agent report: ok=%t report=%+v err=%v", ok, stored, err)
	}
}

type fixedAgentExecutor struct {
	execution AgentExecution
}

func (e fixedAgentExecutor) Execute(context.Context, AgentScenario) (AgentExecution, error) {
	return e.execution, nil
}

func TestAgentRunnerFlagsConsequentialCallWithoutPermission(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	runner, err := NewAgentRunner(AgentRunnerOptions{Executor: fixedAgentExecutor{execution: AgentExecution{
		Events: []core.Event{
			{Type: core.EventToolCallStart, ToolUseID: "call-1", ToolName: "Transfer"},
			{Type: core.EventToolCallEnd, ToolUseID: "call-1", ToolName: "Transfer"},
			{Type: core.EventUsage, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}},
			{Type: core.EventResult, Subtype: "success", StopReason: core.StopEndTurn},
		},
		ToolDescriptors: map[string]core.ToolDescriptor{"Transfer": {
			Risk: core.RiskCritical, SideEffect: core.SideEffectNonIdempotent,
			Idempotency: core.IdempotencyUnsupported,
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(ctx, AgentSuite{
		Name: "unsafe-agent",
		Scenarios: []AgentScenario{{
			ID: "transfer", Expected: AgentExpectedOutcome{
				ToolCalls: []ExpectedToolCall{{Name: "Transfer", Arguments: map[string]interface{}{}}},
			},
		}},
		Gates: []Gate{{Metric: MetricUnsafeActionRate, Operator: GateAtMost, Value: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.UnsafeActionRate.Value != 1 || report.Gates.Passed {
		t.Fatalf("unsafe action was not gated: %+v", report)
	}
}

type concurrencyAgentExecutor struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (e *concurrencyAgentExecutor) Execute(ctx context.Context, _ AgentScenario) (AgentExecution, error) {
	e.mu.Lock()
	e.active++
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
	e.mu.Unlock()
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
		return AgentExecution{}, ctx.Err()
	}
	e.mu.Lock()
	e.active--
	e.mu.Unlock()
	return AgentExecution{Events: []core.Event{
		{Type: core.EventResult, Subtype: "success", StopReason: core.StopEndTurn},
	}}, nil
}

func TestAgentRunnerBoundsConcurrencyAndPreservesCaseOrder(t *testing.T) {
	executor := &concurrencyAgentExecutor{}
	runner, err := NewAgentRunner(AgentRunnerOptions{Executor: executor, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenarios := make([]AgentScenario, 6)
	for index := range scenarios {
		scenarios[index] = AgentScenario{ID: string(rune('a' + index))}
	}
	ctx := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a"})
	report, err := runner.Run(ctx, AgentSuite{Name: "concurrency", Scenarios: scenarios})
	if err != nil {
		t.Fatal(err)
	}
	if executor.maxActive > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", executor.maxActive)
	}
	for index, result := range report.Cases {
		if result.ScenarioID != scenarios[index].ID {
			t.Fatalf("case order changed: %+v", report.Cases)
		}
	}
}

func TestAgentReportRejectsImpossibleToolAndUsageMeasurements(t *testing.T) {
	report := validAgentReport()
	report.Cases[0].ToolCalls = 1
	report.Cases[0].ToolErrors = 2
	if err := report.Validate(); err == nil {
		t.Fatal("expected impossible tool measurements to be rejected")
	}

	report = validAgentReport()
	report.Cases[0].Usage.InputTokens = -1
	if err := report.Validate(); err == nil {
		t.Fatal("expected negative model usage to be rejected")
	}
}

func validAgentReport() AgentReport {
	now := time.Now().UTC()
	return AgentReport{
		SchemaVersion: SchemaVersion,
		ID:            "report",
		TenantID:      "tenant-a",
		SuiteName:     "suite",
		StartedAt:     now,
		CompletedAt:   now,
		Gates:         GateResult{Passed: true},
		Cases:         []AgentCaseResult{{ScenarioID: "case"}},
	}
}
