// Package evaluation provides deterministic regression testing for published
// workflows. It never calls real tools or model providers.
package evaluation

import (
	"fmt"
	"math"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

const SchemaVersion = "1"

type Suite struct {
	Name      string
	Scenarios []Scenario
	Gates     []Gate
}

type Scenario struct {
	ID        string
	Input     map[string]interface{}
	Context   map[string]interface{}
	Principal core.Principal
	Tools     map[string]ToolFixture
	Approvals map[string]policy.ApprovalStatus
	Expected  ExpectedOutcome
}

type ToolFixture struct {
	Descriptor core.ToolDescriptor
	Responses  []ToolResponse
}

type ToolResponse struct {
	Output    interface{}
	Error     string
	Retryable bool
}

type ExpectedOutcome struct {
	Status       workflow.ExecutionStatus
	ErrorKind    core.ErrorKind
	ToolCalls    []ExpectedToolCall
	StepStatuses map[string]workflow.StepStatus
	Validate     func(workflow.Execution) error
}

type ExpectedToolCall struct {
	Name      string
	Arguments map[string]interface{}
}

type Report struct {
	SchemaVersion   string       `json:"schema_version"`
	ID              string       `json:"id"`
	TenantID        string       `json:"tenant_id"`
	SuiteName       string       `json:"suite_name"`
	WorkflowID      string       `json:"workflow_id"`
	WorkflowVersion int          `json:"workflow_version"`
	WorkflowDigest  string       `json:"workflow_digest"`
	StartedAt       time.Time    `json:"started_at"`
	CompletedAt     time.Time    `json:"completed_at"`
	Metrics         Metrics      `json:"metrics"`
	Gates           GateResult   `json:"gates"`
	Cases           []CaseResult `json:"cases"`
}

type Rate struct {
	Value     float64 `json:"value"`
	Numerator int     `json:"numerator"`
	Total     int     `json:"total"`
	Measured  bool    `json:"measured"`
}

type Metrics struct {
	TaskSuccessRate       Rate          `json:"task_success_rate"`
	StepAccuracy          Rate          `json:"step_accuracy"`
	ToolSelectionAccuracy Rate          `json:"tool_selection_accuracy"`
	ParameterAccuracy     Rate          `json:"parameter_accuracy"`
	UnsafeActionRate      Rate          `json:"unsafe_action_rate"`
	HumanInterventionRate Rate          `json:"human_intervention_rate"`
	RetryRate             Rate          `json:"retry_rate"`
	AverageToolCalls      float64       `json:"average_tool_calls"`
	AverageLLMCalls       float64       `json:"average_llm_calls"`
	AverageLatency        time.Duration `json:"average_latency"`
	P95Latency            time.Duration `json:"p95_latency"`
}

type CaseResult struct {
	ScenarioID       string                   `json:"scenario_id"`
	ExecutionID      string                   `json:"execution_id,omitempty"`
	Status           workflow.ExecutionStatus `json:"status"`
	Passed           bool                     `json:"passed"`
	Duration         time.Duration            `json:"duration"`
	ToolCalls        int                      `json:"tool_calls"`
	RetryCount       int                      `json:"retry_count"`
	ApprovalRequests int                      `json:"approval_requests"`
	UnsafeActions    int                      `json:"unsafe_actions"`
	Failures         []Failure                `json:"failures,omitempty"`
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r Report) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported evaluation schema version %q", r.SchemaVersion)
	}
	if r.ID == "" || r.TenantID == "" || r.SuiteName == "" || r.WorkflowID == "" ||
		r.WorkflowVersion < 1 || r.WorkflowDigest == "" {
		return fmt.Errorf("evaluation report requires id, tenant, suite, workflow, version, and digest")
	}
	if r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) {
		return fmt.Errorf("evaluation report has invalid timestamps")
	}
	if len(r.Cases) == 0 {
		return fmt.Errorf("evaluation report requires at least one case")
	}
	rates := []struct {
		name string
		rate Rate
	}{
		{name: "task_success_rate", rate: r.Metrics.TaskSuccessRate},
		{name: "step_accuracy", rate: r.Metrics.StepAccuracy},
		{name: "tool_selection_accuracy", rate: r.Metrics.ToolSelectionAccuracy},
		{name: "parameter_accuracy", rate: r.Metrics.ParameterAccuracy},
		{name: "unsafe_action_rate", rate: r.Metrics.UnsafeActionRate},
		{name: "human_intervention_rate", rate: r.Metrics.HumanInterventionRate},
		{name: "retry_rate", rate: r.Metrics.RetryRate},
	}
	for _, item := range rates {
		if err := validateRate(item.name, item.rate); err != nil {
			return err
		}
	}
	if invalidNonNegative(r.Metrics.AverageToolCalls) || invalidNonNegative(r.Metrics.AverageLLMCalls) ||
		r.Metrics.AverageLatency < 0 || r.Metrics.P95Latency < 0 {
		return fmt.Errorf("evaluation report contains invalid aggregate metrics")
	}
	if r.Gates.Evaluated < 0 || r.Gates.Passed && len(r.Gates.Violations) > 0 ||
		!r.Gates.Passed && len(r.Gates.Violations) == 0 {
		return fmt.Errorf("evaluation report contains inconsistent gate results")
	}
	seenCases := make(map[string]struct{}, len(r.Cases))
	for _, result := range r.Cases {
		if result.ScenarioID == "" {
			return fmt.Errorf("evaluation case requires a scenario id")
		}
		if _, exists := seenCases[result.ScenarioID]; exists {
			return fmt.Errorf("duplicate evaluation scenario result %q", result.ScenarioID)
		}
		seenCases[result.ScenarioID] = struct{}{}
		if result.Duration < 0 || result.ToolCalls < 0 || result.RetryCount < 0 ||
			result.ApprovalRequests < 0 || result.UnsafeActions < 0 {
			return fmt.Errorf("evaluation scenario %q contains negative measurements", result.ScenarioID)
		}
	}
	return nil
}

func validateRate(name string, rate Rate) error {
	if math.IsNaN(rate.Value) || math.IsInf(rate.Value, 0) || rate.Value < 0 || rate.Value > 1 ||
		rate.Numerator < 0 || rate.Total < 0 || rate.Numerator > rate.Total && rate.Total > 0 {
		return fmt.Errorf("evaluation metric %s is invalid", name)
	}
	if !rate.Measured {
		if rate.Value != 0 || rate.Numerator != 0 || rate.Total != 0 {
			return fmt.Errorf("unmeasured evaluation metric %s contains values", name)
		}
		return nil
	}
	if rate.Total == 0 {
		if rate.Value != 0 || rate.Numerator != 0 {
			return fmt.Errorf("zero-sample evaluation metric %s contains values", name)
		}
		return nil
	}
	expected := float64(rate.Numerator) / float64(rate.Total)
	if math.Abs(rate.Value-expected) > 1e-12 {
		return fmt.Errorf("evaluation metric %s value does not match its counts", name)
	}
	return nil
}

func invalidNonNegative(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0) || value < 0
}
