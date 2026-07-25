package evaluation

import (
	"fmt"
	"math"
)

type MetricName string

const (
	MetricTaskSuccessRate       MetricName = "task_success_rate"
	MetricStepAccuracy          MetricName = "step_accuracy"
	MetricToolSelectionAccuracy MetricName = "tool_selection_accuracy"
	MetricParameterAccuracy     MetricName = "parameter_accuracy"
	MetricUnsafeActionRate      MetricName = "unsafe_action_rate"
	MetricHumanInterventionRate MetricName = "human_intervention_rate"
	MetricRetryRate             MetricName = "retry_rate"
	MetricAverageToolCalls      MetricName = "average_tool_calls"
	MetricAverageLLMCalls       MetricName = "average_llm_calls"
	MetricAverageLatencyMS      MetricName = "average_latency_ms"
	MetricP95LatencyMS          MetricName = "p95_latency_ms"
	MetricToolErrorRate         MetricName = "tool_error_rate"
	MetricAverageInputTokens    MetricName = "average_input_tokens"
	MetricAverageOutputTokens   MetricName = "average_output_tokens"
	MetricExtractionSuccessRate MetricName = "extraction_success_rate"
	MetricWorkflowValidityRate  MetricName = "workflow_validity_rate"
	MetricEvidenceAccuracy      MetricName = "evidence_accuracy"
	MetricUnsafeCandidateRate   MetricName = "unsafe_candidate_rate"
)

type GateOperator string

const (
	GateAtLeast GateOperator = "at_least"
	GateAtMost  GateOperator = "at_most"
)

type Gate struct {
	Metric   MetricName   `json:"metric"`
	Operator GateOperator `json:"operator"`
	Value    float64      `json:"value"`
}

type GateResult struct {
	Passed     bool            `json:"passed"`
	Evaluated  int             `json:"evaluated"`
	Violations []GateViolation `json:"violations,omitempty"`
}

type GateViolation struct {
	Metric   MetricName   `json:"metric"`
	Operator GateOperator `json:"operator"`
	Expected float64      `json:"expected"`
	Actual   float64      `json:"actual"`
	Reason   string       `json:"reason"`
}

func EvaluateExtractorGates(metrics ExtractorMetrics, gates []Gate) (GateResult, error) {
	return evaluateNamedGates(gates, func(name MetricName) (float64, bool, error) {
		switch name {
		case MetricTaskSuccessRate:
			return metrics.TaskSuccessRate.Value, metrics.TaskSuccessRate.Measured, nil
		case MetricExtractionSuccessRate:
			return metrics.ExtractionSuccessRate.Value, metrics.ExtractionSuccessRate.Measured, nil
		case MetricWorkflowValidityRate:
			return metrics.WorkflowValidityRate.Value, metrics.WorkflowValidityRate.Measured, nil
		case MetricStepAccuracy:
			return metrics.StepAccuracy.Value, metrics.StepAccuracy.Measured, nil
		case MetricToolSelectionAccuracy:
			return metrics.ToolSelectionAccuracy.Value, metrics.ToolSelectionAccuracy.Measured, nil
		case MetricParameterAccuracy:
			return metrics.ParameterAccuracy.Value, metrics.ParameterAccuracy.Measured, nil
		case MetricEvidenceAccuracy:
			return metrics.EvidenceAccuracy.Value, metrics.EvidenceAccuracy.Measured, nil
		case MetricUnsafeCandidateRate:
			return metrics.UnsafeCandidateRate.Value, metrics.UnsafeCandidateRate.Measured, nil
		case MetricAverageLLMCalls:
			return metrics.AverageLLMCalls, metrics.ModelUsageMeasured, nil
		case MetricAverageInputTokens:
			return metrics.AverageInputTokens, metrics.ModelUsageMeasured, nil
		case MetricAverageOutputTokens:
			return metrics.AverageOutputTokens, metrics.ModelUsageMeasured, nil
		case MetricAverageLatencyMS:
			return float64(metrics.AverageLatency) / 1e6, true, nil
		case MetricP95LatencyMS:
			return float64(metrics.P95Latency) / 1e6, true, nil
		default:
			return 0, false, fmt.Errorf("unsupported extractor evaluation metric %q", name)
		}
	})
}

func EvaluateAgentGates(metrics AgentMetrics, gates []Gate) (GateResult, error) {
	return evaluateNamedGates(gates, func(name MetricName) (float64, bool, error) {
		switch name {
		case MetricTaskSuccessRate:
			return metrics.TaskSuccessRate.Value, metrics.TaskSuccessRate.Measured, nil
		case MetricToolSelectionAccuracy:
			return metrics.ToolSelectionAccuracy.Value, metrics.ToolSelectionAccuracy.Measured, nil
		case MetricParameterAccuracy:
			return metrics.ParameterAccuracy.Value, metrics.ParameterAccuracy.Measured, nil
		case MetricUnsafeActionRate:
			return metrics.UnsafeActionRate.Value, metrics.UnsafeActionRate.Measured, nil
		case MetricToolErrorRate:
			return metrics.ToolErrorRate.Value, metrics.ToolErrorRate.Measured, nil
		case MetricHumanInterventionRate:
			return metrics.HumanInterventionRate.Value, metrics.HumanInterventionRate.Measured, nil
		case MetricAverageToolCalls:
			return metrics.AverageToolCalls, true, nil
		case MetricAverageLLMCalls:
			return metrics.AverageLLMCalls, true, nil
		case MetricAverageInputTokens:
			return metrics.AverageInputTokens, true, nil
		case MetricAverageOutputTokens:
			return metrics.AverageOutputTokens, true, nil
		case MetricAverageLatencyMS:
			return float64(metrics.AverageLatency) / 1e6, true, nil
		case MetricP95LatencyMS:
			return float64(metrics.P95Latency) / 1e6, true, nil
		default:
			return 0, false, fmt.Errorf("unsupported agent evaluation metric %q", name)
		}
	})
}

func EvaluateGates(metrics Metrics, gates []Gate) (GateResult, error) {
	return evaluateNamedGates(gates, func(name MetricName) (float64, bool, error) {
		return metricValue(metrics, name)
	})
}

func evaluateNamedGates(
	gates []Gate,
	value func(MetricName) (float64, bool, error),
) (GateResult, error) {
	result := GateResult{Passed: true, Evaluated: len(gates)}
	for _, gate := range gates {
		if math.IsNaN(gate.Value) || math.IsInf(gate.Value, 0) {
			return GateResult{}, fmt.Errorf("gate %q has a non-finite threshold", gate.Metric)
		}
		actual, measured, err := value(gate.Metric)
		if err != nil {
			return GateResult{}, err
		}
		switch gate.Operator {
		case GateAtLeast, GateAtMost:
		default:
			return GateResult{}, fmt.Errorf("gate %q has invalid operator %q", gate.Metric, gate.Operator)
		}
		if !measured {
			result.Passed = false
			result.Violations = append(result.Violations, GateViolation{
				Metric: gate.Metric, Operator: gate.Operator, Expected: gate.Value,
				Reason: "metric was not measured by any scenario",
			})
			continue
		}
		violated := gate.Operator == GateAtLeast && actual < gate.Value ||
			gate.Operator == GateAtMost && actual > gate.Value
		if violated {
			result.Passed = false
			result.Violations = append(result.Violations, GateViolation{
				Metric: gate.Metric, Operator: gate.Operator,
				Expected: gate.Value, Actual: actual, Reason: "threshold not met",
			})
		}
	}
	return result, nil
}

func metricValue(metrics Metrics, name MetricName) (float64, bool, error) {
	switch name {
	case MetricTaskSuccessRate:
		return metrics.TaskSuccessRate.Value, metrics.TaskSuccessRate.Measured, nil
	case MetricStepAccuracy:
		return metrics.StepAccuracy.Value, metrics.StepAccuracy.Measured, nil
	case MetricToolSelectionAccuracy:
		return metrics.ToolSelectionAccuracy.Value, metrics.ToolSelectionAccuracy.Measured, nil
	case MetricParameterAccuracy:
		return metrics.ParameterAccuracy.Value, metrics.ParameterAccuracy.Measured, nil
	case MetricUnsafeActionRate:
		return metrics.UnsafeActionRate.Value, metrics.UnsafeActionRate.Measured, nil
	case MetricHumanInterventionRate:
		return metrics.HumanInterventionRate.Value, metrics.HumanInterventionRate.Measured, nil
	case MetricRetryRate:
		return metrics.RetryRate.Value, metrics.RetryRate.Measured, nil
	case MetricAverageToolCalls:
		return metrics.AverageToolCalls, true, nil
	case MetricAverageLLMCalls:
		return metrics.AverageLLMCalls, true, nil
	case MetricAverageLatencyMS:
		return float64(metrics.AverageLatency) / 1e6, true, nil
	case MetricP95LatencyMS:
		return float64(metrics.P95Latency) / 1e6, true, nil
	default:
		return 0, false, fmt.Errorf("unsupported evaluation metric %q", name)
	}
}
