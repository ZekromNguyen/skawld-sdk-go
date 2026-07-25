package learning

import (
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func TestAnalyzeFeedbackAggregatesWithoutComments(t *testing.T) {
	items := []workflow.ExecutionFeedback{
		feedbackFixture("one", workflow.FeedbackAccepted, "correct", "", "secret-one"),
		feedbackFixture(
			"two", workflow.FeedbackCorrection, "incorrect.account",
			"select_payable_account", "secret-two",
		),
		feedbackFixture(
			"three", workflow.FeedbackCorrection, "incorrect.account",
			"select_payable_account", "secret-three",
		),
	}
	analysis, err := AnalyzeFeedback(items)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Total != 3 || analysis.Accepted != 1 || analysis.Corrections != 2 ||
		!analysis.RequiresNewDemonstrations ||
		len(analysis.ReasonPatterns) != 2 ||
		analysis.ReasonPatterns[0].Value != "incorrect.account" ||
		analysis.ReasonPatterns[0].Count != 2 ||
		len(analysis.CorrectedActionPatterns) != 1 {
		t.Fatalf("unexpected feedback analysis: %+v", analysis)
	}
}

func TestAnalyzeFeedbackRejectsMixedWorkflowVersions(t *testing.T) {
	items := []workflow.ExecutionFeedback{
		feedbackFixture("one", workflow.FeedbackAccepted, "correct", "", ""),
		feedbackFixture("two", workflow.FeedbackAccepted, "correct", "", ""),
	}
	items[1].WorkflowVersion = 2
	if _, err := AnalyzeFeedback(items); err == nil {
		t.Fatal("expected mixed workflow versions to fail")
	}
}

func feedbackFixture(
	id string,
	disposition workflow.FeedbackDisposition,
	reason, action, comment string,
) workflow.ExecutionFeedback {
	return workflow.ExecutionFeedback{
		SchemaVersion: workflow.SchemaVersion, ID: id, TenantID: "tenant-a",
		ExecutionID: "execution-" + id, WorkflowID: "invoice", WorkflowVersion: 1,
		ExecutionStatus: workflow.ExecutionCompleted,
		Disposition:     disposition, ReasonCode: reason, CorrectedAction: action,
		Comment: comment, CreatedAt: time.Now().UTC(), CreatedBy: "reviewer",
	}
}
