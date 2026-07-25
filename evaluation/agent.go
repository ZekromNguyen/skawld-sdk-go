package evaluation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
)

const maxAgentEvaluationConcurrency = 64

type AgentSuite struct {
	Name      string
	Scenarios []AgentScenario
	Gates     []Gate
}

type AgentScenario struct {
	ID        string
	Prompt    string
	Principal core.Principal
	Expected  AgentExpectedOutcome
}

type AgentExpectedOutcome struct {
	ResultSubtype   string
	StopReason      core.StopReason
	FinalText       *string
	ToolCalls       []ExpectedToolCall
	MaxLLMCalls     *int
	MaxInputTokens  *int
	MaxOutputTokens *int
	Validate        func(AgentExecution) error
}

type AgentExecution struct {
	Events          []core.Event
	Duration        time.Duration
	ToolDescriptors map[string]core.ToolDescriptor
}

type AgentExecutor interface {
	Execute(context.Context, AgentScenario) (AgentExecution, error)
}

// SDKAgentExecutor adapts the SDK Agent runtime to AgentRunner. Factory must
// return an isolated agent for each scenario and must be safe for concurrent
// calls when AgentRunner concurrency is greater than one.
type SDKAgentExecutor struct {
	Factory func(context.Context, AgentScenario) (*skawld.Agent, error)
}

func (e SDKAgentExecutor) Execute(ctx context.Context, scenario AgentScenario) (AgentExecution, error) {
	if e.Factory == nil {
		return AgentExecution{}, core.NewConfigError("SDK agent evaluator requires an agent factory")
	}
	agent, err := e.Factory(ctx, scenario)
	if err != nil {
		return AgentExecution{}, err
	}
	if agent == nil {
		return AgentExecution{}, core.NewConfigError("SDK agent evaluator factory returned nil")
	}
	defer agent.Close()
	principal := scenario.Principal
	if !principal.Valid() {
		principal, _ = core.PrincipalFromContext(ctx)
	}
	session, err := agent.Session(ctx, skawld.SessionOptions{Principal: principal})
	if err != nil {
		return AgentExecution{}, err
	}
	start := time.Now()
	events := make([]core.Event, 0)
	for event := range session.Run(ctx, scenario.Prompt, skawld.RunOptions{}) {
		events = append(events, event)
	}
	descriptors := make(map[string]core.ToolDescriptor)
	options := agent.Options()
	for _, name := range options.Tools.Names() {
		if tool, exists := options.Tools.Get(name); exists {
			descriptors[name] = core.DescribeTool(tool)
		}
	}
	return AgentExecution{
		Events: events, Duration: time.Since(start),
		ToolDescriptors: descriptors,
	}, nil
}

type AgentReport struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	TenantID      string            `json:"tenant_id"`
	SuiteName     string            `json:"suite_name"`
	StartedAt     time.Time         `json:"started_at"`
	CompletedAt   time.Time         `json:"completed_at"`
	Metrics       AgentMetrics      `json:"metrics"`
	Gates         GateResult        `json:"gates"`
	Cases         []AgentCaseResult `json:"cases"`
}

type AgentMetrics struct {
	TaskSuccessRate       Rate          `json:"task_success_rate"`
	ToolSelectionAccuracy Rate          `json:"tool_selection_accuracy"`
	ParameterAccuracy     Rate          `json:"parameter_accuracy"`
	UnsafeActionRate      Rate          `json:"unsafe_action_rate"`
	ToolErrorRate         Rate          `json:"tool_error_rate"`
	HumanInterventionRate Rate          `json:"human_intervention_rate"`
	AverageLLMCalls       float64       `json:"average_llm_calls"`
	AverageToolCalls      float64       `json:"average_tool_calls"`
	AverageInputTokens    float64       `json:"average_input_tokens"`
	AverageOutputTokens   float64       `json:"average_output_tokens"`
	AverageLatency        time.Duration `json:"average_latency"`
	P95Latency            time.Duration `json:"p95_latency"`
}

type AgentCaseResult struct {
	ScenarioID        string          `json:"scenario_id"`
	Passed            bool            `json:"passed"`
	ResultSubtype     string          `json:"result_subtype,omitempty"`
	StopReason        core.StopReason `json:"stop_reason,omitempty"`
	Duration          time.Duration   `json:"duration"`
	LLMCalls          int             `json:"llm_calls"`
	ToolCalls         int             `json:"tool_calls"`
	ToolErrors        int             `json:"tool_errors"`
	UnsafeActions     int             `json:"unsafe_actions"`
	PermissionPrompts int             `json:"permission_prompts"`
	Usage             core.Usage      `json:"usage"`
	Failures          []Failure       `json:"failures,omitempty"`
}

func (r AgentReport) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported agent evaluation schema version %q", r.SchemaVersion)
	}
	if r.ID == "" || r.TenantID == "" || r.SuiteName == "" {
		return fmt.Errorf("agent evaluation report requires id, tenant, and suite")
	}
	if r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) || len(r.Cases) == 0 {
		return fmt.Errorf("agent evaluation report has invalid timestamps or cases")
	}
	rates := []struct {
		name string
		rate Rate
	}{
		{name: "task_success_rate", rate: r.Metrics.TaskSuccessRate},
		{name: "tool_selection_accuracy", rate: r.Metrics.ToolSelectionAccuracy},
		{name: "parameter_accuracy", rate: r.Metrics.ParameterAccuracy},
		{name: "unsafe_action_rate", rate: r.Metrics.UnsafeActionRate},
		{name: "tool_error_rate", rate: r.Metrics.ToolErrorRate},
		{name: "human_intervention_rate", rate: r.Metrics.HumanInterventionRate},
	}
	for _, item := range rates {
		if err := validateRate(item.name, item.rate); err != nil {
			return err
		}
	}
	if invalidNonNegative(r.Metrics.AverageLLMCalls) ||
		invalidNonNegative(r.Metrics.AverageToolCalls) ||
		invalidNonNegative(r.Metrics.AverageInputTokens) ||
		invalidNonNegative(r.Metrics.AverageOutputTokens) ||
		r.Metrics.AverageLatency < 0 || r.Metrics.P95Latency < 0 {
		return fmt.Errorf("agent evaluation report contains invalid aggregate metrics")
	}
	if r.Gates.Evaluated < 0 || r.Gates.Passed && len(r.Gates.Violations) > 0 ||
		!r.Gates.Passed && len(r.Gates.Violations) == 0 {
		return fmt.Errorf("agent evaluation report contains inconsistent gate results")
	}
	seen := make(map[string]struct{}, len(r.Cases))
	for _, result := range r.Cases {
		if result.ScenarioID == "" {
			return fmt.Errorf("agent evaluation case requires a scenario id")
		}
		if _, exists := seen[result.ScenarioID]; exists {
			return fmt.Errorf("duplicate agent evaluation scenario result %q", result.ScenarioID)
		}
		seen[result.ScenarioID] = struct{}{}
		if result.Duration < 0 || result.LLMCalls < 0 || result.ToolCalls < 0 ||
			result.ToolErrors < 0 || result.UnsafeActions < 0 || result.PermissionPrompts < 0 {
			return fmt.Errorf("agent evaluation scenario %q contains negative measurements", result.ScenarioID)
		}
		if result.ToolErrors > result.ToolCalls || result.UnsafeActions > result.ToolCalls {
			return fmt.Errorf("agent evaluation scenario %q contains inconsistent tool measurements", result.ScenarioID)
		}
		if !validUsage(result.Usage) {
			return fmt.Errorf("agent evaluation scenario %q contains invalid model usage", result.ScenarioID)
		}
	}
	return nil
}

type AgentRunnerOptions struct {
	Executor       AgentExecutor
	Store          AgentStore
	MaxConcurrency int
	Now            func() time.Time
}

type AgentRunner struct {
	executor       AgentExecutor
	store          AgentStore
	maxConcurrency int
	now            func() time.Time
}

func NewAgentRunner(options AgentRunnerOptions) (*AgentRunner, error) {
	if options.Executor == nil {
		return nil, core.NewConfigError("agent evaluation requires an executor")
	}
	if options.MaxConcurrency == 0 {
		options.MaxConcurrency = 1
	}
	if options.MaxConcurrency < 1 || options.MaxConcurrency > maxAgentEvaluationConcurrency {
		return nil, core.NewConfigError("agent evaluation concurrency must be between 1 and 64")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &AgentRunner{
		executor: options.Executor, store: options.Store,
		maxConcurrency: options.MaxConcurrency, now: options.Now,
	}, nil
}

func (r *AgentRunner) Run(ctx context.Context, suite AgentSuite) (AgentReport, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return AgentReport{}, core.NewPermissionError("agent evaluation requires an authenticated tenant")
	}
	if suite.Name == "" || len(suite.Scenarios) == 0 {
		return AgentReport{}, core.NewConfigError("agent evaluation suite requires a name and scenarios")
	}
	if err := validateAgentScenarios(suite.Scenarios, principal); err != nil {
		return AgentReport{}, err
	}
	startedAt := r.now()
	caseResults := make([]AgentCaseResult, len(suite.Scenarios))
	caseMetrics := make([]agentCaseMetrics, len(suite.Scenarios))
	jobs := make(chan int)
	var workers sync.WaitGroup
	var errorMu sync.Mutex
	var firstError error
	recordError := func(err error) {
		errorMu.Lock()
		defer errorMu.Unlock()
		if firstError == nil {
			firstError = err
		}
	}
	workerCount := min(r.maxConcurrency, len(suite.Scenarios))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				scenario := suite.Scenarios[index]
				if err := ctx.Err(); err != nil {
					recordError(err)
					continue
				}
				execution, err := r.executor.Execute(core.WithPrincipal(ctx, scenario.Principal), scenario)
				if err != nil {
					recordError(fmt.Errorf("scenario %q: %w", scenario.ID, err))
					continue
				}
				caseResults[index], caseMetrics[index] = evaluateAgentScenario(scenario, execution)
			}
		}()
	}
enqueue:
	for index := range suite.Scenarios {
		select {
		case jobs <- index:
		case <-ctx.Done():
			recordError(ctx.Err())
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	if firstError != nil {
		return AgentReport{}, firstError
	}
	metrics := aggregateAgentMetrics(caseResults, caseMetrics)
	gates, err := EvaluateAgentGates(metrics, suite.Gates)
	if err != nil {
		return AgentReport{}, err
	}
	report := AgentReport{
		SchemaVersion: SchemaVersion, ID: id.New(), TenantID: principal.TenantID,
		SuiteName: suite.Name, StartedAt: startedAt, CompletedAt: r.now(),
		Metrics: metrics, Gates: gates, Cases: caseResults,
	}
	if report.CompletedAt.Before(report.StartedAt) {
		report.CompletedAt = report.StartedAt
	}
	if err := report.Validate(); err != nil {
		return AgentReport{}, err
	}
	if r.store != nil {
		if err := r.store.SaveAgentReport(ctx, report); err != nil {
			return AgentReport{}, err
		}
	}
	return report, nil
}

type agentCaseMetrics struct {
	toolMatches, toolTotal   int
	paramMatches, paramTotal int
}

func evaluateAgentScenario(scenario AgentScenario, execution AgentExecution) (AgentCaseResult, agentCaseMetrics) {
	result := AgentCaseResult{ScenarioID: scenario.ID, Duration: execution.Duration}
	metrics := agentCaseMetrics{}
	var finalText string
	actualCalls := make([]actualToolCall, 0)
	permissionIDs := make(map[string]struct{})
	for _, event := range execution.Events {
		switch event.Type {
		case core.EventUsage:
			result.LLMCalls++
			result.Usage = core.AddUsage(result.Usage, event.Usage)
		case core.EventToolCallStart:
			result.ToolCalls++
			actualCalls = append(actualCalls, actualToolCall{name: event.ToolName, arguments: cloneMap(event.Input)})
		case core.EventToolCallEnd:
			if event.IsError {
				result.ToolErrors++
			}
		case core.EventPermissionRequest:
			result.PermissionPrompts++
			for _, request := range event.Requests {
				permissionIDs[request.ToolUseID] = struct{}{}
			}
		case core.EventResult:
			result.ResultSubtype = event.Subtype
			result.StopReason = event.StopReason
			finalText = event.FinalText
		}
	}
	for _, event := range execution.Events {
		if event.Type != core.EventToolCallStart {
			continue
		}
		descriptor := execution.ToolDescriptors[event.ToolName]
		if consequentialTool(descriptor) {
			if _, approved := permissionIDs[event.ToolUseID]; !approved {
				result.UnsafeActions++
			}
		}
	}

	expectedSubtype := scenario.Expected.ResultSubtype
	if expectedSubtype == "" {
		expectedSubtype = "success"
	}
	if result.ResultSubtype != expectedSubtype {
		result.Failures = append(result.Failures, Failure{
			Code:    "result_mismatch",
			Message: fmt.Sprintf("expected result %q, got %q", expectedSubtype, result.ResultSubtype),
		})
	}
	if scenario.Expected.StopReason != "" && result.StopReason != scenario.Expected.StopReason {
		result.Failures = append(result.Failures, Failure{
			Code:    "stop_reason_mismatch",
			Message: fmt.Sprintf("expected stop reason %q, got %q", scenario.Expected.StopReason, result.StopReason),
		})
	}
	if scenario.Expected.FinalText != nil && finalText != *scenario.Expected.FinalText {
		result.Failures = append(result.Failures, Failure{
			Code: "final_text_mismatch", Message: "final response differs from expectation",
		})
	}
	if scenario.Expected.MaxLLMCalls != nil && result.LLMCalls > *scenario.Expected.MaxLLMCalls {
		result.Failures = append(result.Failures, Failure{
			Code: "llm_call_budget_exceeded", Message: "LLM call budget exceeded",
		})
	}
	if scenario.Expected.MaxInputTokens != nil && result.Usage.InputTokens > *scenario.Expected.MaxInputTokens {
		result.Failures = append(result.Failures, Failure{
			Code: "input_token_budget_exceeded", Message: "input token budget exceeded",
		})
	}
	if scenario.Expected.MaxOutputTokens != nil && result.Usage.OutputTokens > *scenario.Expected.MaxOutputTokens {
		result.Failures = append(result.Failures, Failure{
			Code: "output_token_budget_exceeded", Message: "output token budget exceeded",
		})
	}
	if scenario.Expected.ToolCalls != nil {
		metrics.toolTotal = max(len(scenario.Expected.ToolCalls), len(actualCalls))
		if metrics.toolTotal == 0 {
			metrics.toolMatches, metrics.toolTotal = 1, 1
			metrics.paramMatches, metrics.paramTotal = 1, 1
		}
		for index := 0; index < min(len(scenario.Expected.ToolCalls), len(actualCalls)); index++ {
			if scenario.Expected.ToolCalls[index].Name == actualCalls[index].name {
				metrics.toolMatches++
			}
			matches, total := compareArguments(scenario.Expected.ToolCalls[index], actualCalls[index])
			metrics.paramMatches += matches
			metrics.paramTotal += total
		}
		for index := len(actualCalls); index < len(scenario.Expected.ToolCalls); index++ {
			metrics.paramTotal += max(1, len(flattenMap(scenario.Expected.ToolCalls[index].Arguments)))
		}
		for index := len(scenario.Expected.ToolCalls); index < len(actualCalls); index++ {
			metrics.paramTotal += max(1, len(flattenMap(actualCalls[index].arguments)))
		}
		if metrics.toolMatches != metrics.toolTotal {
			result.Failures = append(result.Failures, Failure{
				Code: "tool_selection_mismatch", Message: "actual tool-call sequence differs from expectation",
			})
		}
		if metrics.paramMatches != metrics.paramTotal {
			result.Failures = append(result.Failures, Failure{
				Code: "parameter_mismatch", Message: "one or more tool arguments differ from expectation",
			})
		}
	}
	if scenario.Expected.Validate != nil {
		if err := scenario.Expected.Validate(execution); err != nil {
			result.Failures = append(result.Failures, Failure{
				Code: "assertion_failed", Message: "application assertion failed",
			})
		}
	}
	result.Passed = len(result.Failures) == 0
	return result, metrics
}

func consequentialTool(descriptor core.ToolDescriptor) bool {
	return descriptor.Risk == core.RiskHigh || descriptor.Risk == core.RiskCritical ||
		descriptor.SideEffect == core.SideEffectUnknown ||
		descriptor.SideEffect == core.SideEffectNonIdempotent ||
		descriptor.NetworkAccess
}

func validateAgentScenarios(scenarios []AgentScenario, principal core.Principal) error {
	seen := make(map[string]struct{}, len(scenarios))
	for index := range scenarios {
		if scenarios[index].ID == "" {
			return core.NewConfigError("agent evaluation scenario id is required")
		}
		if _, exists := seen[scenarios[index].ID]; exists {
			return core.NewConfigError(fmt.Sprintf("duplicate agent evaluation scenario %q", scenarios[index].ID))
		}
		seen[scenarios[index].ID] = struct{}{}
		if !scenarios[index].Principal.Valid() {
			scenarios[index].Principal = principal
		}
		if scenarios[index].Principal.TenantID != principal.TenantID {
			return core.NewPermissionError("agent evaluation scenario belongs to another tenant")
		}
		if scenarios[index].Expected.MaxLLMCalls != nil && *scenarios[index].Expected.MaxLLMCalls < 0 ||
			scenarios[index].Expected.MaxInputTokens != nil && *scenarios[index].Expected.MaxInputTokens < 0 ||
			scenarios[index].Expected.MaxOutputTokens != nil && *scenarios[index].Expected.MaxOutputTokens < 0 {
			return core.NewConfigError("agent evaluation budgets must not be negative")
		}
	}
	return nil
}

func aggregateAgentMetrics(results []AgentCaseResult, details []agentCaseMetrics) AgentMetrics {
	taskSuccess := 0
	toolMatches, toolTotal := 0, 0
	paramMatches, paramTotal := 0, 0
	unsafe, toolCalls, toolErrors, intervention := 0, 0, 0, 0
	llmCalls, inputTokens, outputTokens := 0, 0, 0
	latencies := make([]time.Duration, 0, len(results))
	for index, result := range results {
		if result.Passed {
			taskSuccess++
		}
		toolMatches += details[index].toolMatches
		toolTotal += details[index].toolTotal
		paramMatches += details[index].paramMatches
		paramTotal += details[index].paramTotal
		unsafe += result.UnsafeActions
		toolCalls += result.ToolCalls
		toolErrors += result.ToolErrors
		if result.PermissionPrompts > 0 {
			intervention++
		}
		llmCalls += result.LLMCalls
		inputTokens += result.Usage.InputTokens
		outputTokens += result.Usage.OutputTokens
		latencies = append(latencies, result.Duration)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	totalLatency := time.Duration(0)
	for _, latency := range latencies {
		totalLatency += latency
	}
	count := len(results)
	return AgentMetrics{
		TaskSuccessRate:       makeRate(taskSuccess, count, false),
		ToolSelectionAccuracy: makeRate(toolMatches, toolTotal, false),
		ParameterAccuracy:     makeRate(paramMatches, paramTotal, false),
		UnsafeActionRate:      makeRate(unsafe, toolCalls, true),
		ToolErrorRate:         makeRate(toolErrors, toolCalls, true),
		HumanInterventionRate: makeRate(intervention, count, true),
		AverageLLMCalls:       averageInt(llmCalls, count),
		AverageToolCalls:      averageInt(toolCalls, count),
		AverageInputTokens:    averageInt(inputTokens, count),
		AverageOutputTokens:   averageInt(outputTokens, count),
		AverageLatency:        averageDuration(totalLatency, count),
		P95Latency:            percentile95(latencies),
	}
}

func averageInt(total, count int) float64 {
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

func averageDuration(total time.Duration, count int) time.Duration {
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

func percentile95(sorted []time.Duration) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := (95*len(sorted) + 99) / 100
	return sorted[index-1]
}
