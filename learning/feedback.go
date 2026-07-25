package learning

import (
	"fmt"
	"sort"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type FeedbackPattern struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// FeedbackAnalysis is a deterministic, content-minimizing summary. Free-form
// comments are intentionally excluded so this value can be used for release
// review and to decide whether new demonstrations are needed.
type FeedbackAnalysis struct {
	WorkflowID                string            `json:"workflow_id"`
	WorkflowVersion           int               `json:"workflow_version"`
	Total                     int               `json:"total"`
	Accepted                  int               `json:"accepted"`
	Corrections               int               `json:"corrections"`
	Failures                  int               `json:"failures"`
	Unsafe                    int               `json:"unsafe"`
	ReasonPatterns            []FeedbackPattern `json:"reason_patterns,omitempty"`
	CorrectedActionPatterns   []FeedbackPattern `json:"corrected_action_patterns,omitempty"`
	RequiresNewDemonstrations bool              `json:"requires_new_demonstrations"`
}

// AnalyzeFeedback aggregates labels for exactly one workflow version. It does
// not infer executable changes or send feedback to a model.
func AnalyzeFeedback(
	feedback []workflow.ExecutionFeedback,
) (FeedbackAnalysis, error) {
	if len(feedback) == 0 {
		return FeedbackAnalysis{}, core.NewConfigError(
			"feedback analysis requires at least one feedback record",
		)
	}
	analysis := FeedbackAnalysis{
		WorkflowID: feedback[0].WorkflowID, WorkflowVersion: feedback[0].WorkflowVersion,
		Total: len(feedback),
	}
	tenantID := feedback[0].TenantID
	reasons := make(map[string]int)
	actions := make(map[string]int)
	for _, item := range feedback {
		if err := item.Validate(); err != nil {
			return FeedbackAnalysis{}, &core.SkawldError{
				Kind: core.ErrorValidation, Message: "invalid workflow feedback", Cause: err,
			}
		}
		if item.TenantID != tenantID || item.WorkflowID != analysis.WorkflowID ||
			item.WorkflowVersion != analysis.WorkflowVersion {
			return FeedbackAnalysis{}, &core.SkawldError{
				Kind:    core.ErrorValidation,
				Message: "feedback analysis requires one tenant and workflow version",
			}
		}
		reasons[item.ReasonCode]++
		switch item.Disposition {
		case workflow.FeedbackAccepted:
			analysis.Accepted++
		case workflow.FeedbackCorrection:
			analysis.Corrections++
			actions[item.CorrectedAction]++
		case workflow.FeedbackFailure:
			analysis.Failures++
		case workflow.FeedbackUnsafe:
			analysis.Unsafe++
		default:
			return FeedbackAnalysis{}, fmt.Errorf(
				"unsupported feedback disposition %q", item.Disposition,
			)
		}
	}
	analysis.ReasonPatterns = sortedFeedbackPatterns(reasons)
	analysis.CorrectedActionPatterns = sortedFeedbackPatterns(actions)
	analysis.RequiresNewDemonstrations =
		analysis.Corrections > 0 || analysis.Failures > 0 || analysis.Unsafe > 0
	return analysis, nil
}

func sortedFeedbackPatterns(counts map[string]int) []FeedbackPattern {
	output := make([]FeedbackPattern, 0, len(counts))
	for value, count := range counts {
		output = append(output, FeedbackPattern{Value: value, Count: count})
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].Count == output[j].Count {
			return output[i].Value < output[j].Value
		}
		return output[i].Count > output[j].Count
	})
	return output
}
