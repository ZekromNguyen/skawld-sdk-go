package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type ModelUsage struct {
	Model    core.ModelID
	LLMCalls int
	Usage    core.Usage
}

type ExtractionExecution struct {
	Candidate workflow.Version
	Usage     *ModelUsage
	Duration  time.Duration
}

type ExtractorExecutor interface {
	Execute(context.Context, learning.ExtractionRequest) (ExtractionExecution, error)
}

type LearningExtractorExecutor struct {
	Extractor learning.Extractor
}

func (e LearningExtractorExecutor) Execute(
	ctx context.Context,
	request learning.ExtractionRequest,
) (ExtractionExecution, error) {
	if e.Extractor == nil {
		return ExtractionExecution{}, core.NewConfigError("extractor evaluation requires an extractor")
	}
	start := time.Now()
	if detailed, ok := e.Extractor.(learning.DetailedExtractor); ok {
		result, err := detailed.ExtractDetailed(ctx, request)
		return ExtractionExecution{
			Candidate: result.Candidate,
			Usage: &ModelUsage{
				Model: result.Model, LLMCalls: result.LLMCalls, Usage: result.Usage,
			},
			Duration: time.Since(start),
		}, err
	}
	candidate, err := e.Extractor.Extract(ctx, request)
	return ExtractionExecution{Candidate: candidate, Duration: time.Since(start)}, err
}

type ExtractorSuite struct {
	Name      string
	Scenarios []ExtractorScenario
	Gates     []Gate
}

type ExtractorScenario struct {
	ID       string
	Request  learning.ExtractionRequest
	Expected workflow.Version
	// SecurityCritical marks an adversarial scenario whose only safe outcome is
	// rejection. It requires ExpectError and contributes to UnsafeCandidateRate.
	SecurityCritical  bool
	ExpectError       bool
	ExpectedErrorKind core.ErrorKind
}

type ExtractorMetrics struct {
	TaskSuccessRate       Rate          `json:"task_success_rate"`
	ExtractionSuccessRate Rate          `json:"extraction_success_rate"`
	WorkflowValidityRate  Rate          `json:"workflow_validity_rate"`
	StepAccuracy          Rate          `json:"step_accuracy"`
	ToolSelectionAccuracy Rate          `json:"tool_selection_accuracy"`
	ParameterAccuracy     Rate          `json:"parameter_accuracy"`
	EvidenceAccuracy      Rate          `json:"evidence_accuracy"`
	UnsafeCandidateRate   Rate          `json:"unsafe_candidate_rate"`
	ModelUsageMeasured    bool          `json:"model_usage_measured"`
	AverageLLMCalls       float64       `json:"average_llm_calls"`
	AverageInputTokens    float64       `json:"average_input_tokens"`
	AverageOutputTokens   float64       `json:"average_output_tokens"`
	AverageLatency        time.Duration `json:"average_latency"`
	P95Latency            time.Duration `json:"p95_latency"`
}

type ExtractorReport struct {
	SchemaVersion string                `json:"schema_version"`
	ID            string                `json:"id"`
	TenantID      string                `json:"tenant_id"`
	SuiteName     string                `json:"suite_name"`
	StartedAt     time.Time             `json:"started_at"`
	CompletedAt   time.Time             `json:"completed_at"`
	Metrics       ExtractorMetrics      `json:"metrics"`
	Gates         GateResult            `json:"gates"`
	Cases         []ExtractorCaseResult `json:"cases"`
}

type ExtractorCaseResult struct {
	ScenarioID       string        `json:"scenario_id"`
	Passed           bool          `json:"passed"`
	Duration         time.Duration `json:"duration"`
	Errored          bool          `json:"errored"`
	Valid            bool          `json:"valid"`
	SecurityCritical bool          `json:"security_critical,omitempty"`
	UnsafeCandidate  bool          `json:"unsafe_candidate,omitempty"`
	LLMCalls         *int          `json:"llm_calls,omitempty"`
	Usage            *core.Usage   `json:"usage,omitempty"`
	Failures         []Failure     `json:"failures,omitempty"`
}

type ExtractorRunnerOptions struct {
	Executor       ExtractorExecutor
	Store          ExtractorStore
	MaxConcurrency int
	Now            func() time.Time
}

type ExtractorRunner struct {
	executor       ExtractorExecutor
	store          ExtractorStore
	maxConcurrency int
	now            func() time.Time
}

func NewExtractorRunner(options ExtractorRunnerOptions) (*ExtractorRunner, error) {
	if options.Executor == nil {
		return nil, core.NewConfigError("extractor evaluation requires an executor")
	}
	if options.MaxConcurrency == 0 {
		options.MaxConcurrency = 1
	}
	if options.MaxConcurrency < 1 || options.MaxConcurrency > maxAgentEvaluationConcurrency {
		return nil, core.NewConfigError("extractor evaluation concurrency must be between 1 and 64")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &ExtractorRunner{
		executor: options.Executor, store: options.Store,
		maxConcurrency: options.MaxConcurrency, now: options.Now,
	}, nil
}

func (r *ExtractorRunner) Run(ctx context.Context, suite ExtractorSuite) (ExtractorReport, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return ExtractorReport{}, core.NewPermissionError("extractor evaluation requires an authenticated tenant")
	}
	if suite.Name == "" || len(suite.Scenarios) == 0 {
		return ExtractorReport{}, core.NewConfigError("extractor evaluation suite requires a name and scenarios")
	}
	if err := validateExtractorScenarios(suite.Scenarios, principal); err != nil {
		return ExtractorReport{}, err
	}
	startedAt := r.now()
	results := make([]ExtractorCaseResult, len(suite.Scenarios))
	details := make([]extractorCaseMetrics, len(suite.Scenarios))
	jobs := make(chan int)
	var workers sync.WaitGroup
	var errorMu sync.Mutex
	var firstInfrastructureError error
	recordError := func(err error) {
		errorMu.Lock()
		defer errorMu.Unlock()
		if firstInfrastructureError == nil {
			firstInfrastructureError = err
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
				execution, extractionErr := r.executor.Execute(
					core.WithPrincipal(ctx, principal), scenario.Request,
				)
				results[index], details[index] = evaluateExtractionScenario(
					scenario, execution, extractionErr,
				)
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
	if firstInfrastructureError != nil {
		return ExtractorReport{}, firstInfrastructureError
	}
	metrics := aggregateExtractorMetrics(results, details)
	gates, err := EvaluateExtractorGates(metrics, suite.Gates)
	if err != nil {
		return ExtractorReport{}, err
	}
	reportID, err := id.New()
	if err != nil {
		return ExtractorReport{}, err
	}
	report := ExtractorReport{
		SchemaVersion: SchemaVersion, ID: reportID, TenantID: principal.TenantID,
		SuiteName: suite.Name, StartedAt: startedAt, CompletedAt: r.now(),
		Metrics: metrics, Gates: gates, Cases: results,
	}
	if report.CompletedAt.Before(report.StartedAt) {
		report.CompletedAt = report.StartedAt
	}
	if err := report.Validate(); err != nil {
		return ExtractorReport{}, err
	}
	if r.store != nil {
		if err := r.store.SaveExtractorReport(ctx, report); err != nil {
			return ExtractorReport{}, err
		}
	}
	return report, nil
}

type extractorCaseMetrics struct {
	extractionSucceeded bool
	valid               bool
	stepMatches         int
	stepTotal           int
	toolMatches         int
	toolTotal           int
	paramMatches        int
	paramTotal          int
	evidenceMatches     int
	evidenceTotal       int
	securityCritical    bool
	unsafeCandidate     bool
}

func evaluateExtractionScenario(
	scenario ExtractorScenario,
	execution ExtractionExecution,
	extractionErr error,
) (ExtractorCaseResult, extractorCaseMetrics) {
	result := ExtractorCaseResult{
		ScenarioID: scenario.ID, Duration: execution.Duration, Errored: extractionErr != nil,
		SecurityCritical: scenario.SecurityCritical,
	}
	metrics := extractorCaseMetrics{
		extractionSucceeded: extractionErr == nil,
		securityCritical:    scenario.SecurityCritical,
		unsafeCandidate:     scenario.SecurityCritical && extractionErr == nil,
	}
	result.UnsafeCandidate = metrics.unsafeCandidate
	if execution.Usage != nil {
		calls := execution.Usage.LLMCalls
		usage := execution.Usage.Usage
		result.LLMCalls = &calls
		result.Usage = &usage
	}
	if extractionErr != nil {
		if !scenario.ExpectError {
			result.Failures = append(result.Failures, Failure{
				Code: "unexpected_extraction_error", Message: "extractor returned an error",
			})
		} else if scenario.ExpectedErrorKind != "" {
			var typed *core.SkawldError
			if !errors.As(extractionErr, &typed) || typed.Kind != scenario.ExpectedErrorKind {
				result.Failures = append(result.Failures, Failure{
					Code:    "unexpected_extraction_error_kind",
					Message: "extractor rejected the scenario for an unexpected reason",
				})
			}
		}
		result.Passed = scenario.ExpectError && len(result.Failures) == 0
		return result, metrics
	}
	if scenario.ExpectError {
		result.Failures = append(result.Failures, Failure{
			Code: "expected_extraction_error", Message: "extractor unexpectedly succeeded",
		})
	}
	candidate := normalizeExtractionCandidate(scenario.Request, execution.Candidate)
	if err := candidate.Validate(); err != nil {
		result.Failures = append(result.Failures, Failure{
			Code: "invalid_workflow", Message: "extractor produced an invalid workflow",
		})
	} else {
		result.Valid = true
		metrics.valid = true
	}
	if len(scenario.Expected.Steps) > 0 {
		compareExtractorCandidate(scenario.Expected, candidate, &result, &metrics)
	}
	result.Passed = len(result.Failures) == 0
	return result, metrics
}

func normalizeExtractionCandidate(request learning.ExtractionRequest, candidate workflow.Version) workflow.Version {
	candidate.SchemaVersion = workflow.SchemaVersion
	candidate.Workflow.ID = request.WorkflowID
	candidate.Workflow.Name = request.WorkflowName
	candidate.Workflow.TenantID = request.TenantID
	candidate.Version = request.NextVersion
	if candidate.Version < 1 {
		candidate.Version = 1
	}
	candidate.Status = workflow.VersionCandidate
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Unix(1, 0).UTC()
	}
	return candidate
}

func compareExtractorCandidate(
	expected workflow.Version,
	actual workflow.Version,
	result *ExtractorCaseResult,
	metrics *extractorCaseMetrics,
) {
	metrics.stepTotal = max(len(expected.Steps), len(actual.Steps))
	if metrics.stepTotal == 0 {
		metrics.stepMatches, metrics.stepTotal = 1, 1
	}
	expectedTools := make([]string, 0)
	actualTools := make([]string, 0)
	for index := 0; index < min(len(expected.Steps), len(actual.Steps)); index++ {
		if stepSemanticFingerprint(expected.Steps[index]) == stepSemanticFingerprint(actual.Steps[index]) {
			metrics.stepMatches++
		}
		compareStepArguments(expected.Steps[index], actual.Steps[index], metrics)
		compareStepEvidence(expected.Steps[index], actual.Steps[index], metrics)
	}
	for _, step := range expected.Steps {
		if step.Tool != nil {
			expectedTools = append(expectedTools, step.Tool.Name)
		}
	}
	for _, step := range actual.Steps {
		if step.Tool != nil {
			actualTools = append(actualTools, step.Tool.Name)
		}
	}
	metrics.toolTotal = max(len(expectedTools), len(actualTools))
	if metrics.toolTotal == 0 {
		metrics.toolMatches, metrics.toolTotal = 1, 1
	}
	for index := 0; index < min(len(expectedTools), len(actualTools)); index++ {
		if expectedTools[index] == actualTools[index] {
			metrics.toolMatches++
		}
	}
	for index := len(actual.Steps); index < len(expected.Steps); index++ {
		countMissingStepExpectations(expected.Steps[index], metrics)
	}
	for index := len(expected.Steps); index < len(actual.Steps); index++ {
		countMissingStepExpectations(actual.Steps[index], metrics)
	}
	if metrics.stepMatches != metrics.stepTotal {
		result.Failures = append(result.Failures, Failure{
			Code: "step_mismatch", Message: "extracted workflow steps differ from expectation",
		})
	}
	if metrics.toolMatches != metrics.toolTotal {
		result.Failures = append(result.Failures, Failure{
			Code: "tool_selection_mismatch", Message: "extracted tool sequence differs from expectation",
		})
	}
	if metrics.paramMatches != metrics.paramTotal {
		result.Failures = append(result.Failures, Failure{
			Code: "parameter_mapping_mismatch", Message: "extracted parameter mappings differ from expectation",
		})
	}
	if metrics.evidenceMatches != metrics.evidenceTotal {
		result.Failures = append(result.Failures, Failure{
			Code: "evidence_mismatch", Message: "extracted evidence differs from expectation",
		})
	}
}

func stepSemanticFingerprint(step workflow.Step) string {
	step.Evidence = nil
	if step.Tool != nil {
		tool := *step.Tool
		tool.Arguments = nil
		step.Tool = &tool
	}
	raw, _ := json.Marshal(step)
	return string(raw)
}

func compareStepArguments(expected, actual workflow.Step, metrics *extractorCaseMetrics) {
	if expected.Tool == nil && actual.Tool == nil {
		return
	}
	expectedArguments := map[string]workflow.Value{}
	actualArguments := map[string]workflow.Value{}
	if expected.Tool != nil {
		expectedArguments = expected.Tool.Arguments
	}
	if actual.Tool != nil {
		actualArguments = actual.Tool.Arguments
	}
	keys := make(map[string]struct{}, len(expectedArguments)+len(actualArguments))
	for key := range expectedArguments {
		keys[key] = struct{}{}
	}
	for key := range actualArguments {
		keys[key] = struct{}{}
	}
	if len(keys) == 0 {
		metrics.paramMatches++
		metrics.paramTotal++
		return
	}
	for key := range keys {
		metrics.paramTotal++
		if valuesEqual(expectedArguments[key], actualArguments[key]) {
			metrics.paramMatches++
		}
	}
}

func compareStepEvidence(expected, actual workflow.Step, metrics *extractorCaseMetrics) {
	expectedEvidence := evidenceFingerprints(expected.Evidence)
	actualEvidence := evidenceFingerprints(actual.Evidence)
	keys := make(map[string]struct{}, len(expectedEvidence)+len(actualEvidence))
	for key := range expectedEvidence {
		keys[key] = struct{}{}
	}
	for key := range actualEvidence {
		keys[key] = struct{}{}
	}
	if len(keys) == 0 {
		metrics.evidenceMatches++
		metrics.evidenceTotal++
		return
	}
	for key := range keys {
		metrics.evidenceTotal++
		_, expectedExists := expectedEvidence[key]
		_, actualExists := actualEvidence[key]
		if expectedExists && actualExists {
			metrics.evidenceMatches++
		}
	}
}

func evidenceFingerprints(evidence []workflow.EvidenceRef) map[string]struct{} {
	output := make(map[string]struct{})
	for _, reference := range evidence {
		events := append([]string(nil), reference.EventIDs...)
		sort.Strings(events)
		raw, _ := json.Marshal([]interface{}{reference.DemonstrationID, events})
		output[string(raw)] = struct{}{}
	}
	return output
}

func countMissingStepExpectations(step workflow.Step, metrics *extractorCaseMetrics) {
	if step.Tool != nil {
		metrics.paramTotal += max(1, len(step.Tool.Arguments))
	}
	metrics.evidenceTotal += max(1, len(step.Evidence))
}

func validateExtractorScenarios(scenarios []ExtractorScenario, principal core.Principal) error {
	seen := make(map[string]struct{}, len(scenarios))
	for _, scenario := range scenarios {
		if scenario.ID == "" || scenario.Request.WorkflowID == "" ||
			scenario.Request.WorkflowName == "" || scenario.Request.TenantID == "" {
			return core.NewConfigError("extractor scenario requires id and workflow identity")
		}
		if _, exists := seen[scenario.ID]; exists {
			return core.NewConfigError(fmt.Sprintf("duplicate extractor evaluation scenario %q", scenario.ID))
		}
		seen[scenario.ID] = struct{}{}
		if scenario.Request.TenantID != principal.TenantID {
			return core.NewPermissionError("extractor evaluation scenario belongs to another tenant")
		}
		if scenario.SecurityCritical && !scenario.ExpectError {
			return core.NewConfigError(
				fmt.Sprintf("security-critical extractor scenario %q must expect rejection", scenario.ID),
			)
		}
		if scenario.SecurityCritical && scenario.ExpectedErrorKind == "" {
			return core.NewConfigError(
				fmt.Sprintf("security-critical extractor scenario %q requires an expected error kind", scenario.ID),
			)
		}
	}
	return nil
}

func aggregateExtractorMetrics(
	results []ExtractorCaseResult,
	details []extractorCaseMetrics,
) ExtractorMetrics {
	taskSuccess, extractionSuccess, valid := 0, 0, 0
	stepMatches, stepTotal, toolMatches, toolTotal := 0, 0, 0, 0
	paramMatches, paramTotal, evidenceMatches, evidenceTotal := 0, 0, 0, 0
	unsafeCandidates, securityScenarios := 0, 0
	usageMeasured := true
	llmCalls, inputTokens, outputTokens := 0, 0, 0
	latencies := make([]time.Duration, 0, len(results))
	for index, result := range results {
		if result.Passed {
			taskSuccess++
		}
		if details[index].extractionSucceeded {
			extractionSuccess++
		}
		if details[index].valid {
			valid++
		}
		stepMatches += details[index].stepMatches
		stepTotal += details[index].stepTotal
		toolMatches += details[index].toolMatches
		toolTotal += details[index].toolTotal
		paramMatches += details[index].paramMatches
		paramTotal += details[index].paramTotal
		evidenceMatches += details[index].evidenceMatches
		evidenceTotal += details[index].evidenceTotal
		if details[index].securityCritical {
			securityScenarios++
			if details[index].unsafeCandidate {
				unsafeCandidates++
			}
		}
		if result.LLMCalls == nil || result.Usage == nil {
			usageMeasured = false
		} else {
			llmCalls += *result.LLMCalls
			inputTokens += result.Usage.InputTokens
			outputTokens += result.Usage.OutputTokens
		}
		latencies = append(latencies, result.Duration)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	totalLatency := time.Duration(0)
	for _, latency := range latencies {
		totalLatency += latency
	}
	count := len(results)
	metrics := ExtractorMetrics{
		TaskSuccessRate:       makeRate(taskSuccess, count, false),
		ExtractionSuccessRate: makeRate(extractionSuccess, count, false),
		WorkflowValidityRate:  makeRate(valid, count, false),
		StepAccuracy:          makeRate(stepMatches, stepTotal, false),
		ToolSelectionAccuracy: makeRate(toolMatches, toolTotal, false),
		ParameterAccuracy:     makeRate(paramMatches, paramTotal, false),
		EvidenceAccuracy:      makeRate(evidenceMatches, evidenceTotal, false),
		UnsafeCandidateRate:   makeRate(unsafeCandidates, securityScenarios, false),
		ModelUsageMeasured:    usageMeasured,
		AverageLatency:        averageDuration(totalLatency, count),
		P95Latency:            percentile95(latencies),
	}
	if usageMeasured {
		metrics.AverageLLMCalls = averageInt(llmCalls, count)
		metrics.AverageInputTokens = averageInt(inputTokens, count)
		metrics.AverageOutputTokens = averageInt(outputTokens, count)
	}
	return metrics
}

func (r ExtractorReport) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.ID == "" || r.TenantID == "" || r.SuiteName == "" {
		return fmt.Errorf("extractor evaluation report has invalid identity")
	}
	if r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) || len(r.Cases) == 0 {
		return fmt.Errorf("extractor evaluation report has invalid timestamps or cases")
	}
	rates := []struct {
		name string
		rate Rate
	}{
		{name: "task_success_rate", rate: r.Metrics.TaskSuccessRate},
		{name: "extraction_success_rate", rate: r.Metrics.ExtractionSuccessRate},
		{name: "workflow_validity_rate", rate: r.Metrics.WorkflowValidityRate},
		{name: "step_accuracy", rate: r.Metrics.StepAccuracy},
		{name: "tool_selection_accuracy", rate: r.Metrics.ToolSelectionAccuracy},
		{name: "parameter_accuracy", rate: r.Metrics.ParameterAccuracy},
		{name: "evidence_accuracy", rate: r.Metrics.EvidenceAccuracy},
		{name: "unsafe_candidate_rate", rate: r.Metrics.UnsafeCandidateRate},
	}
	for _, item := range rates {
		if err := validateRate(item.name, item.rate); err != nil {
			return err
		}
	}
	if invalidNonNegative(r.Metrics.AverageLLMCalls) ||
		invalidNonNegative(r.Metrics.AverageInputTokens) ||
		invalidNonNegative(r.Metrics.AverageOutputTokens) ||
		r.Metrics.AverageLatency < 0 || r.Metrics.P95Latency < 0 {
		return fmt.Errorf("extractor evaluation report contains invalid aggregate metrics")
	}
	if r.Gates.Evaluated < 0 || r.Gates.Passed && len(r.Gates.Violations) > 0 ||
		!r.Gates.Passed && len(r.Gates.Violations) == 0 {
		return fmt.Errorf("extractor evaluation report contains inconsistent gate results")
	}
	seen := make(map[string]struct{}, len(r.Cases))
	for _, result := range r.Cases {
		if result.ScenarioID == "" || result.Duration < 0 {
			return fmt.Errorf("extractor evaluation case is invalid")
		}
		if _, exists := seen[result.ScenarioID]; exists {
			return fmt.Errorf("duplicate extractor evaluation scenario result %q", result.ScenarioID)
		}
		seen[result.ScenarioID] = struct{}{}
		if (result.LLMCalls == nil) != (result.Usage == nil) {
			return fmt.Errorf("extractor evaluation scenario %q has incomplete model usage", result.ScenarioID)
		}
		if result.LLMCalls != nil && (*result.LLMCalls < 0 || !validUsage(*result.Usage)) {
			return fmt.Errorf("extractor evaluation scenario %q contains invalid model usage", result.ScenarioID)
		}
	}
	return nil
}

func validUsage(usage core.Usage) bool {
	return usage.InputTokens >= 0 &&
		usage.OutputTokens >= 0 &&
		usage.CacheReadTokens >= 0 &&
		usage.CacheCreationTokens >= 0
}
