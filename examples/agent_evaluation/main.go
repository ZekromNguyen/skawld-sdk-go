package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

func main() {
	principal := core.Principal{TenantID: "demo-company", ActorID: "release-reviewer"}
	runner, err := evaluation.NewAgentRunner(evaluation.AgentRunnerOptions{
		MaxConcurrency: 2,
		Executor: evaluation.SDKAgentExecutor{
			Factory: func(_ context.Context, scenario evaluation.AgentScenario) (*skawld.Agent, error) {
				registry := tools.NewRegistry()
				if err := registry.Register(invoiceLookup{}); err != nil {
					return nil, err
				}
				return skawld.NewAgent(skawld.AgentOptions{
					Provider:         &fixtureProvider{},
					Model:            "fixture-model",
					Tools:            registry,
					Principal:        scenario.Principal,
					MaxTurns:         4,
					DisableSkills:    true,
					DisableSubagents: true,
					ProviderRetry:    &skawld.ProviderRetryPolicy{MaxRetries: 0},
				})
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	finalText := "Invoice is ready for review."
	maxCalls := 2
	report, err := runner.Run(core.WithPrincipal(context.Background(), principal), evaluation.AgentSuite{
		Name: "invoice-agent-release",
		Scenarios: []evaluation.AgentScenario{{
			ID:     "lookup-invoice",
			Prompt: "Find invoice INV-1001 and summarize its status.",
			Expected: evaluation.AgentExpectedOutcome{
				StopReason:  core.StopEndTurn,
				FinalText:   &finalText,
				MaxLLMCalls: &maxCalls,
				ToolCalls: []evaluation.ExpectedToolCall{{
					Name: "invoice.lookup",
					Arguments: map[string]interface{}{
						"invoice_id": "INV-1001",
					},
				}},
			},
		}},
		Gates: []evaluation.Gate{
			{Metric: evaluation.MetricTaskSuccessRate, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricToolSelectionAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricParameterAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricUnsafeActionRate, Operator: evaluation.GateAtMost, Value: 0},
			{Metric: evaluation.MetricAverageLLMCalls, Operator: evaluation.GateAtMost, Value: 2},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	output, _ := json.MarshalIndent(map[string]interface{}{
		"report_id":               report.ID,
		"passed":                  report.Gates.Passed,
		"task_success_rate":       report.Metrics.TaskSuccessRate.Value,
		"tool_selection_accuracy": report.Metrics.ToolSelectionAccuracy.Value,
		"parameter_accuracy":      report.Metrics.ParameterAccuracy.Value,
		"unsafe_action_rate":      report.Metrics.UnsafeActionRate.Value,
		"average_llm_calls":       report.Metrics.AverageLLMCalls,
		"average_input_tokens":    report.Metrics.AverageInputTokens,
		"average_output_tokens":   report.Metrics.AverageOutputTokens,
	}, "", "  ")
	fmt.Println(string(output))
}

type fixtureProvider struct {
	turn int
}

func (*fixtureProvider) ID() string                     { return "fixture-provider" }
func (*fixtureProvider) ContextWindow(core.ModelID) int { return 100_000 }

func (p *fixtureProvider) Stream(
	ctx context.Context,
	request core.ProviderRequest,
) core.ProviderStream {
	output := make(chan core.ProviderStreamResult, 8)
	p.turn++
	turn := p.turn
	go func() {
		defer close(output)
		send := func(event core.ProviderStreamEvent) bool {
			select {
			case output <- core.ProviderStreamResult{Event: event}:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !send(core.ProviderStreamEvent{Type: "message_start", Model: request.Model}) {
			return
		}
		if turn == 1 {
			if !send(core.ProviderStreamEvent{
				Type: "tool_use_start", ID: "lookup-1", Name: "invoice.lookup",
			}) || !send(core.ProviderStreamEvent{
				Type: "tool_use_input_delta", ID: "lookup-1",
				JSONDelta: `{"invoice_id":"INV-1001"}`,
			}) || !send(core.ProviderStreamEvent{
				Type: "tool_use_end", ID: "lookup-1",
			}) {
				return
			}
			send(core.ProviderStreamEvent{
				Type: "message_end", StopReason: core.StopToolUse,
				Usage: core.Usage{InputTokens: 20, OutputTokens: 4},
			})
			return
		}
		if !send(core.ProviderStreamEvent{
			Type: "text_delta", Text: "Invoice is ready for review.",
		}) {
			return
		}
		send(core.ProviderStreamEvent{
			Type: "message_end", StopReason: core.StopEndTurn,
			Usage: core.Usage{InputTokens: 12, OutputTokens: 6},
		})
	}()
	return output
}

type invoiceLookup struct{}

func (invoiceLookup) Name() string        { return "invoice.lookup" }
func (invoiceLookup) Description() string { return "Look up an invoice by ID." }
func (invoiceLookup) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"invoice_id": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"invoice_id"},
	}
}
func (invoiceLookup) Scope() core.ToolScope { return core.ToolScopeRead }
func (invoiceLookup) ParallelSafe() bool    { return true }
func (invoiceLookup) Validate(input map[string]interface{}) (map[string]interface{}, error) {
	if _, ok := input["invoice_id"].(string); !ok {
		return nil, core.NewConfigError("invoice_id is required")
	}
	return input, nil
}
func (invoiceLookup) Execute(map[string]interface{}, core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{
		Content: map[string]interface{}{"status": "ready_for_review"},
	}, nil
}
func (invoiceLookup) Summarize(map[string]interface{}) string { return "look up invoice" }
func (invoiceLookup) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk:        core.RiskLow,
		SideEffect:  core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
	}
}
