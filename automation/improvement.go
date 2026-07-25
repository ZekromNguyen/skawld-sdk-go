package automation

import (
	"context"
	"sort"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type ImprovementRequest struct {
	WorkflowKey     string
	WorkflowID      string
	BaseVersion     int
	MinimumFeedback int
	Learning        learning.MultiDemoOptions
}

type ImprovementProposal struct {
	BaseVersion         int                        `json:"base_version"`
	Feedback            learning.FeedbackAnalysis  `json:"feedback"`
	NewDemonstrationIDs []string                   `json:"new_demonstration_ids"`
	Compilation         learning.CompilationResult `json:"compilation"`
}

// ProposeImprovement converts correction/failure/unsafe feedback plus newly
// reviewed demonstrations into a new candidate. It does not evaluate, review,
// publish, route, or execute the candidate.
func (l *Lifecycle) ProposeImprovement(
	ctx context.Context,
	request ImprovementRequest,
	principal core.Principal,
) (ImprovementProposal, error) {
	if !authenticatedIdentity(ctx, principal) {
		return ImprovementProposal{}, core.NewPermissionError(
			"workflow improvement requires the authenticated actor identity",
		)
	}
	request.WorkflowKey = strings.TrimSpace(request.WorkflowKey)
	request.WorkflowID = strings.TrimSpace(request.WorkflowID)
	if request.WorkflowKey == "" || request.WorkflowID == "" ||
		request.BaseVersion < 1 {
		return ImprovementProposal{}, core.NewConfigError(
			"workflow improvement requires workflow key, workflow id, and base version",
		)
	}
	if request.MinimumFeedback == 0 {
		request.MinimumFeedback = 1
	}
	if request.MinimumFeedback < 1 || request.MinimumFeedback > 1000 {
		return ImprovementProposal{}, core.NewConfigError(
			"workflow improvement minimum feedback must be between 1 and 1000",
		)
	}
	if l.feedback == nil {
		return ImprovementProposal{}, core.NewConfigError(
			"automation lifecycle feedback store is not configured",
		)
	}
	base, exists, err := l.workflows.Get(
		ctx, request.WorkflowID, request.BaseVersion,
	)
	if err != nil {
		return ImprovementProposal{}, err
	}
	if !exists {
		return ImprovementProposal{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "base workflow version not found",
		}
	}
	feedback, err := l.feedback.List(ctx, workflow.FeedbackFilter{
		WorkflowID: request.WorkflowID, WorkflowVersion: request.BaseVersion,
		Limit: 1000,
	})
	if err != nil {
		return ImprovementProposal{}, err
	}
	if len(feedback) < request.MinimumFeedback {
		return ImprovementProposal{}, &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "not enough execution feedback to propose an improvement",
		}
	}
	feedbackAnalysis, err := learning.AnalyzeFeedback(feedback)
	if err != nil {
		return ImprovementProposal{}, err
	}
	if !feedbackAnalysis.RequiresNewDemonstrations {
		return ImprovementProposal{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "feedback does not support changing executable behavior",
		}
	}
	demonstrations, err := l.demonstrations.List(ctx, request.WorkflowKey)
	if err != nil {
		return ImprovementProposal{}, err
	}
	completed := make([]observation.Demonstration, 0, len(demonstrations))
	previous := make(map[string]struct{}, len(base.SourceDemonstrationIDs))
	for _, demonstrationID := range base.SourceDemonstrationIDs {
		previous[demonstrationID] = struct{}{}
	}
	newIDs := make([]string, 0)
	for _, demonstration := range demonstrations {
		if demonstration.Status != observation.DemonstrationCompleted {
			continue
		}
		completed = append(completed, demonstration)
		if _, exists := previous[demonstration.ID]; !exists {
			newIDs = append(newIDs, demonstration.ID)
		}
	}
	if len(newIDs) == 0 {
		return ImprovementProposal{}, &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "workflow improvement requires at least one new completed demonstration",
		}
	}
	sort.Strings(newIDs)
	compilation, err := l.compiler.CompileMultiple(
		ctx, base.Workflow.ID, base.Workflow.Name, completed, request.Learning,
	)
	if err != nil {
		return ImprovementProposal{}, err
	}
	return ImprovementProposal{
		BaseVersion: request.BaseVersion, Feedback: feedbackAnalysis,
		NewDemonstrationIDs: newIDs, Compilation: compilation,
	}, nil
}
