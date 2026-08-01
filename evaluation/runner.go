package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type RunnerOptions struct {
	Policy policy.Evaluator
	Store  Store
	Now    func() time.Time
}

type Runner struct {
	policy policy.Evaluator
	store  Store
	now    func() time.Time
}

func NewRunner(options RunnerOptions) *Runner {
	if options.Policy == nil {
		options.Policy = policy.RiskPolicy{}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{policy: options.Policy, store: options.Store, now: options.Now}
}

type actualToolCall struct {
	name      string
	arguments map[string]interface{}
}

type fixtureToolRunner struct {
	fixtures map[string]ToolFixture
	offsets  map[string]int
	calls    []actualToolCall
	// catalogDigest attests that fixtures intentionally simulate this exact
	// compiled candidate contract; publication independently verifies the live
	// registry before release.
	catalogDigest string
}

func newFixtureToolRunner(fixtures map[string]ToolFixture, catalogDigest string) (*fixtureToolRunner, error) {
	copied := make(map[string]ToolFixture, len(fixtures))
	for name, fixture := range fixtures {
		if name == "" {
			return nil, core.NewConfigError("evaluation fixture tool name is required")
		}
		if fixture.Descriptor.Risk == "" || fixture.Descriptor.SideEffect == "" || fixture.Descriptor.Idempotency == "" {
			return nil, core.NewConfigError(fmt.Sprintf("evaluation fixture %q has incomplete safety metadata", name))
		}
		copied[name] = fixture
	}
	return &fixtureToolRunner{
		fixtures: copied, offsets: make(map[string]int), catalogDigest: catalogDigest,
	}, nil
}

func (r *fixtureToolRunner) ToolCatalogFingerprint(
	ctx context.Context,
	_ []string,
) (string, error) {
	return r.catalogDigest, ctx.Err()
}

func (r *fixtureToolRunner) Describe(ctx context.Context, name string) (core.ToolDescriptor, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolDescriptor{}, false, err
	}
	fixture, ok := r.fixtures[name]
	return fixture.Descriptor, ok, nil
}

func (r *fixtureToolRunner) Execute(
	ctx context.Context,
	name string,
	input map[string]interface{},
	_ string,
) (workflow.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return workflow.ToolResult{}, err
	}
	fixture, ok := r.fixtures[name]
	if !ok {
		return workflow.ToolResult{}, fmt.Errorf("fixture tool %q is not configured", name)
	}
	r.calls = append(r.calls, actualToolCall{name: name, arguments: cloneMap(input)})
	offset := r.offsets[name]
	r.offsets[name] = offset + 1
	if len(fixture.Responses) == 0 {
		return workflow.ToolResult{}, nil
	}
	if offset >= len(fixture.Responses) {
		return workflow.ToolResult{}, fmt.Errorf("fixture responses exhausted for tool %q", name)
	}
	response := fixture.Responses[offset]
	result := workflow.ToolResult{Output: cloneValue(response.Output), Retryable: response.Retryable}
	if response.Error != "" {
		return result, errors.New(response.Error)
	}
	return result, nil
}

type metricCounters struct {
	taskSuccess, taskTotal           int
	stepMatches, stepTotal           int
	toolMatches, toolTotal           int
	paramMatches, paramTotal         int
	unsafeActions, actualCalls       int
	intervenedCases                  int
	retriedSteps, attemptedToolSteps int
	totalToolCalls                   int
	latencies                        []time.Duration
}

func (r *Runner) Run(ctx context.Context, suite Suite, version workflow.Version) (Report, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return Report{}, core.NewPermissionError("evaluation requires an authenticated tenant")
	}
	if version.Status != workflow.VersionCandidate && version.Status != workflow.VersionPublished {
		return Report{}, core.NewConfigError("evaluation requires a candidate or published workflow version")
	}
	if err := version.Validate(); err != nil {
		return Report{}, &core.SkawldError{Kind: core.ErrorValidation, Message: "invalid workflow version", Cause: err}
	}
	if version.Workflow.TenantID != "" && version.Workflow.TenantID != principal.TenantID {
		return Report{}, core.NewPermissionError("workflow belongs to another tenant")
	}
	if suite.Name == "" || len(suite.Scenarios) == 0 {
		return Report{}, core.NewConfigError("evaluation suite requires a name and at least one scenario")
	}
	executableVersion := version
	executableVersion.Status = workflow.VersionPublished
	if err := validateScenarioIDs(suite.Scenarios); err != nil {
		return Report{}, err
	}
	digest, err := workflow.Digest(version)
	if err != nil {
		return Report{}, &core.SkawldError{
			Kind: core.ErrorValidation, Message: "workflow is not serializable", Cause: err,
		}
	}

	startedAt := r.now()
	reportID, err := id.New()
	if err != nil {
		return Report{}, err
	}
	report := Report{
		SchemaVersion: SchemaVersion, ID: reportID, TenantID: principal.TenantID,
		SuiteName: suite.Name, WorkflowID: version.Workflow.ID, WorkflowVersion: version.Version,
		WorkflowDigest: digest, StartedAt: startedAt,
		Cases: make([]CaseResult, 0, len(suite.Scenarios)),
	}
	counters := metricCounters{taskTotal: len(suite.Scenarios)}
	for _, scenario := range suite.Scenarios {
		caseResult, caseMetrics, err := r.runScenario(ctx, executableVersion, principal, scenario)
		if err != nil {
			return Report{}, fmt.Errorf("evaluate scenario %q: %w", scenario.ID, err)
		}
		report.Cases = append(report.Cases, caseResult)
		accumulateMetrics(&counters, caseResult, caseMetrics)
	}
	report.Metrics = counters.metrics()
	gates, err := EvaluateGates(report.Metrics, suite.Gates)
	if err != nil {
		return Report{}, err
	}
	report.Gates = gates
	report.CompletedAt = r.now()
	if report.CompletedAt.Before(report.StartedAt) {
		report.CompletedAt = report.StartedAt
	}
	if err := report.Validate(); err != nil {
		return Report{}, err
	}
	if r.store != nil {
		if err := r.store.Save(ctx, report); err != nil {
			return Report{}, err
		}
	}
	return report, nil
}

type caseMetrics struct {
	stepMatches, stepTotal   int
	toolMatches, toolTotal   int
	paramMatches, paramTotal int
	attemptedToolSteps       int
	retriedSteps             int
}

func (r *Runner) runScenario(
	ctx context.Context,
	version workflow.Version,
	defaultPrincipal core.Principal,
	scenario Scenario,
) (CaseResult, caseMetrics, error) {
	principal := scenario.Principal
	if !principal.Valid() {
		principal = defaultPrincipal
	}
	if principal.TenantID != defaultPrincipal.TenantID {
		return CaseResult{}, caseMetrics{}, core.NewPermissionError("evaluation scenario belongs to another tenant")
	}
	for stepID, decision := range scenario.Approvals {
		if decision != policy.ApprovalGranted && decision != policy.ApprovalRejected {
			return CaseResult{}, caseMetrics{}, core.NewConfigError(
				fmt.Sprintf("scenario %q approval for step %q must be granted or rejected", scenario.ID, stepID),
			)
		}
	}
	tools, err := newFixtureToolRunner(scenario.Tools, version.ToolCatalogDigest)
	if err != nil {
		return CaseResult{}, caseMetrics{}, err
	}
	approvals := policy.NewMemoryApprovalStore()
	auditStore := &audit.MemoryStore{}
	executor, err := workflow.NewExecutor(workflow.ExecutorOptions{
		Tools: tools, Policy: r.policy, Approvals: approvals, Audit: auditStore, Now: r.now,
	})
	if err != nil {
		return CaseResult{}, caseMetrics{}, err
	}

	caseStarted := r.now()
	execution, executeErr := executor.Execute(
		core.WithPrincipal(ctx, principal), version, cloneMap(scenario.Input), cloneMap(scenario.Context), principal,
	)
	approvalRequests := 0
	for executeErr == nil && execution.Status == workflow.ExecutionAwaitingApproval {
		if approvalRequests > len(version.Steps) {
			return CaseResult{}, caseMetrics{}, fmt.Errorf("approval resume limit exceeded")
		}
		approvalRequests++
		stepID := version.Steps[execution.NextStep].ID
		decision, exists := scenario.Approvals[stepID]
		if !exists {
			decision = policy.ApprovalRejected
		}
		if _, err := approvals.Decide(
			core.WithPrincipal(ctx, principal), execution.PendingApprovalID, decision, principal,
			"deterministic evaluation decision",
		); err != nil {
			return CaseResult{}, caseMetrics{}, err
		}
		execution, executeErr = executor.Resume(core.WithPrincipal(ctx, principal), version, execution)
	}
	caseCompleted := r.now()
	duration := caseCompleted.Sub(caseStarted)
	if duration < 0 {
		duration = 0
	}
	result := CaseResult{
		ScenarioID: scenario.ID, ExecutionID: execution.ID, Status: execution.Status,
		Duration: duration, ToolCalls: len(tools.calls), ApprovalRequests: approvalRequests,
	}
	if executeErr != nil {
		result.Failures = append(result.Failures, Failure{Code: "execution_error", Message: "workflow executor returned an infrastructure error"})
	}

	events, err := auditStore.List(core.WithPrincipal(ctx, principal), execution.ID)
	if err != nil {
		return CaseResult{}, caseMetrics{}, err
	}
	result.UnsafeActions = countUnsafeActions(events, execution, scenario.Tools)
	for index, stepRun := range execution.Steps {
		if index >= len(version.Steps) || version.Steps[index].Kind != workflow.StepTool || stepRun.Attempts == 0 {
			continue
		}
		if stepRun.Attempts > 1 {
			result.RetryCount += stepRun.Attempts - 1
		}
	}
	metrics := compareOutcome(version, scenario.Expected, execution, tools.calls, &result)
	result.Passed = len(result.Failures) == 0
	return result, metrics, nil
}

func compareOutcome(
	version workflow.Version,
	expected ExpectedOutcome,
	execution workflow.Execution,
	calls []actualToolCall,
	result *CaseResult,
) caseMetrics {
	metrics := caseMetrics{}
	expectedStatus := expected.Status
	if expectedStatus == "" {
		expectedStatus = workflow.ExecutionCompleted
	}
	if execution.Status != expectedStatus {
		result.Failures = append(result.Failures, Failure{
			Code:    "status_mismatch",
			Message: fmt.Sprintf("expected status %q, got %q", expectedStatus, execution.Status),
		})
	}
	if expected.ErrorKind != "" {
		actualKind := core.ErrorKind("")
		if execution.Error != nil {
			actualKind = execution.Error.Kind
		}
		if actualKind != expected.ErrorKind {
			result.Failures = append(result.Failures, Failure{
				Code:    "error_kind_mismatch",
				Message: fmt.Sprintf("expected error kind %q, got %q", expected.ErrorKind, actualKind),
			})
		}
	}

	if expected.ToolCalls != nil {
		metrics.toolTotal = max(len(expected.ToolCalls), len(calls))
		if metrics.toolTotal == 0 {
			metrics.toolTotal, metrics.toolMatches = 1, 1
			metrics.paramTotal, metrics.paramMatches = 1, 1
		}
		for index := 0; index < min(len(expected.ToolCalls), len(calls)); index++ {
			if expected.ToolCalls[index].Name == calls[index].name {
				metrics.toolMatches++
			}
			matches, total := compareArguments(expected.ToolCalls[index], calls[index])
			metrics.paramMatches += matches
			metrics.paramTotal += total
		}
		for index := len(calls); index < len(expected.ToolCalls); index++ {
			metrics.paramTotal += max(1, len(flattenMap(expected.ToolCalls[index].Arguments)))
		}
		for index := len(expected.ToolCalls); index < len(calls); index++ {
			metrics.paramTotal += max(1, len(flattenMap(calls[index].arguments)))
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

	if expected.StepStatuses != nil {
		actual := make(map[string]workflow.StepStatus, len(execution.Steps))
		for _, step := range execution.Steps {
			actual[step.StepID] = step.Status
		}
		metrics.stepTotal = len(expected.StepStatuses)
		for stepID, status := range expected.StepStatuses {
			if actual[stepID] == status {
				metrics.stepMatches++
			} else {
				result.Failures = append(result.Failures, Failure{
					Code:    "step_status_mismatch",
					Message: fmt.Sprintf("step %q expected status %q, got %q", stepID, status, actual[stepID]),
				})
			}
		}
	}
	if expected.Validate != nil {
		if err := expected.Validate(execution); err != nil {
			result.Failures = append(result.Failures, Failure{
				Code: "assertion_failed", Message: "application assertion failed",
			})
		}
	}
	for index, run := range execution.Steps {
		if index >= len(version.Steps) || version.Steps[index].Kind != workflow.StepTool || run.Attempts == 0 {
			continue
		}
		metrics.attemptedToolSteps++
		if run.Attempts > 1 {
			metrics.retriedSteps++
		}
	}
	return metrics
}

func countUnsafeActions(events []audit.Event, execution workflow.Execution, fixtures map[string]ToolFixture) int {
	count := 0
	for _, event := range events {
		if event.Type != audit.EventToolCalled {
			continue
		}
		descriptor := fixtures[event.ToolName].Descriptor
		consequential := descriptor.Risk == core.RiskHigh || descriptor.Risk == core.RiskCritical ||
			descriptor.SideEffect == core.SideEffectUnknown ||
			descriptor.SideEffect == core.SideEffectNonIdempotent ||
			descriptor.NetworkAccess
		if consequential && execution.Approvals[event.StepID] == "" {
			count++
		}
	}
	return count
}

func compareArguments(expected ExpectedToolCall, actual actualToolCall) (int, int) {
	expectedFields := flattenMap(expected.Arguments)
	actualFields := flattenMap(actual.arguments)
	keys := make(map[string]struct{}, len(expectedFields)+len(actualFields))
	for key := range expectedFields {
		keys[key] = struct{}{}
	}
	for key := range actualFields {
		keys[key] = struct{}{}
	}
	if len(keys) == 0 {
		return 1, 1
	}
	matches := 0
	for key := range keys {
		expectedValue, expectedExists := expectedFields[key]
		actualValue, actualExists := actualFields[key]
		if expectedExists && actualExists && valuesEqual(expectedValue, actualValue) {
			matches++
		}
	}
	return matches, len(keys)
}

func flattenMap(input map[string]interface{}) map[string]interface{} {
	output := make(map[string]interface{})
	var visit func(string, interface{})
	visit = func(path string, value interface{}) {
		if object, ok := value.(map[string]interface{}); ok {
			keys := make([]string, 0, len(object))
			for key := range object {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				next := key
				if path != "" {
					next = path + "." + key
				}
				visit(next, object[key])
			}
			return
		}
		output[path] = value
	}
	for key, value := range input {
		visit(key, value)
	}
	return output
}

func valuesEqual(left, right interface{}) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func validateScenarioIDs(scenarios []Scenario) error {
	seen := make(map[string]struct{}, len(scenarios))
	for _, scenario := range scenarios {
		if scenario.ID == "" {
			return core.NewConfigError("evaluation scenario id is required")
		}
		if _, exists := seen[scenario.ID]; exists {
			return core.NewConfigError(fmt.Sprintf("duplicate evaluation scenario %q", scenario.ID))
		}
		seen[scenario.ID] = struct{}{}
	}
	return nil
}

func accumulateMetrics(counters *metricCounters, result CaseResult, metrics caseMetrics) {
	if result.Passed {
		counters.taskSuccess++
	}
	counters.stepMatches += metrics.stepMatches
	counters.stepTotal += metrics.stepTotal
	counters.toolMatches += metrics.toolMatches
	counters.toolTotal += metrics.toolTotal
	counters.paramMatches += metrics.paramMatches
	counters.paramTotal += metrics.paramTotal
	counters.unsafeActions += result.UnsafeActions
	counters.actualCalls += result.ToolCalls
	if result.ApprovalRequests > 0 {
		counters.intervenedCases++
	}
	counters.retriedSteps += metrics.retriedSteps
	counters.attemptedToolSteps += metrics.attemptedToolSteps
	counters.totalToolCalls += result.ToolCalls
	counters.latencies = append(counters.latencies, result.Duration)
}

func (c metricCounters) metrics() Metrics {
	latencies := append([]time.Duration(nil), c.latencies...)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	totalLatency := time.Duration(0)
	for _, latency := range latencies {
		totalLatency += latency
	}
	averageLatency := time.Duration(0)
	p95Latency := time.Duration(0)
	if len(latencies) > 0 {
		averageLatency = totalLatency / time.Duration(len(latencies))
		index := (95*len(latencies) + 99) / 100
		p95Latency = latencies[index-1]
	}
	averageToolCalls := 0.0
	if c.taskTotal > 0 {
		averageToolCalls = float64(c.totalToolCalls) / float64(c.taskTotal)
	}
	return Metrics{
		TaskSuccessRate:       makeRate(c.taskSuccess, c.taskTotal, false),
		StepAccuracy:          makeRate(c.stepMatches, c.stepTotal, false),
		ToolSelectionAccuracy: makeRate(c.toolMatches, c.toolTotal, false),
		ParameterAccuracy:     makeRate(c.paramMatches, c.paramTotal, false),
		UnsafeActionRate:      makeRate(c.unsafeActions, c.actualCalls, true),
		HumanInterventionRate: makeRate(c.intervenedCases, c.taskTotal, true),
		RetryRate:             makeRate(c.retriedSteps, c.attemptedToolSteps, true),
		AverageToolCalls:      averageToolCalls,
		AverageLLMCalls:       0,
		AverageLatency:        averageLatency,
		P95Latency:            p95Latency,
	}
}

func makeRate(numerator, total int, zeroWhenUnmeasured bool) Rate {
	if total == 0 {
		return Rate{Value: 0, Numerator: numerator, Measured: zeroWhenUnmeasured}
	}
	return Rate{
		Value:     float64(numerator) / float64(total),
		Numerator: numerator, Total: total, Measured: true,
	}
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	raw, _ := json.Marshal(input)
	var output map[string]interface{}
	_ = json.Unmarshal(raw, &output)
	return output
}

func cloneValue(input interface{}) interface{} {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var output interface{}
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil
	}
	return output
}
