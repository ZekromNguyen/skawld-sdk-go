package evaluation

import (
	"context"
	"fmt"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type PublisherOptions struct {
	Workflows     workflow.Store
	Reports       Store
	Reviews       workflow.ReviewStore
	ToolCatalog   workflow.ToolCatalogFingerprinter
	Audit         audit.Sink
	RequiredSuite string
	MaxReportAge  time.Duration
	Now           func() time.Time
}

// Publisher is an opt-in publication boundary that requires the latest
// matching evaluation report to pass its configured release gates.
type Publisher struct {
	workflows     workflow.Store
	reports       Store
	reviews       workflow.ReviewStore
	toolCatalog   workflow.ToolCatalogFingerprinter
	audit         audit.Sink
	requiredSuite string
	maxReportAge  time.Duration
	now           func() time.Time
}

func NewPublisher(options PublisherOptions) (*Publisher, error) {
	if options.Workflows == nil || options.Reports == nil || options.Reviews == nil {
		return nil, core.NewConfigError("evaluation publisher requires workflow, report, and review stores")
	}
	if options.MaxReportAge < 0 {
		return nil, core.NewConfigError("evaluation maximum report age must not be negative")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Publisher{
		workflows: options.Workflows, reports: options.Reports, reviews: options.Reviews,
		toolCatalog: options.ToolCatalog, audit: options.Audit,
		requiredSuite: options.RequiredSuite, maxReportAge: options.MaxReportAge, now: options.Now,
	}, nil
}

func (p *Publisher) Publish(
	ctx context.Context,
	workflowID string,
	versionNumber int,
	principal core.Principal,
) (workflow.Version, error) {
	authenticated, ok := core.PrincipalFromContext(ctx)
	if !ok || authenticated.TenantID == "" ||
		authenticated.TenantID != principal.TenantID ||
		authenticated.ActorID == "" || authenticated.ActorID != principal.ActorID {
		return workflow.Version{}, core.NewPermissionError(
			"evaluation publication requires the authenticated publisher identity",
		)
	}
	candidate, exists, err := p.workflows.Get(ctx, workflowID, versionNumber)
	if err != nil {
		return workflow.Version{}, err
	}
	if !exists {
		return workflow.Version{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: "workflow candidate not found"}
	}
	if candidate.Status != workflow.VersionCandidate {
		return workflow.Version{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "only candidate workflows can be evaluation-published"}
	}
	if candidate.ToolCatalogDigest != "" {
		if p.toolCatalog == nil {
			return workflow.Version{}, &core.SkawldError{
				Kind:    core.ErrorPolicy,
				Message: "learned workflow publication requires tool catalog verification",
			}
		}
		current, err := p.toolCatalog.ToolCatalogFingerprint(
			ctx, workflow.ReferencedToolNames(candidate),
		)
		if err != nil {
			return workflow.Version{}, err
		}
		if current != candidate.ToolCatalogDigest {
			return workflow.Version{}, &core.SkawldError{
				Kind:    core.ErrorPolicy,
				Message: "tool contracts changed after workflow compilation",
			}
		}
	}
	reports, err := p.reports.List(ctx, workflowID, versionNumber)
	if err != nil {
		return workflow.Version{}, err
	}
	var latest *Report
	for index := range reports {
		report := &reports[index]
		if p.requiredSuite != "" && report.SuiteName != p.requiredSuite {
			continue
		}
		if latest == nil || report.CompletedAt.After(latest.CompletedAt) ||
			report.CompletedAt.Equal(latest.CompletedAt) && report.ID > latest.ID {
			latest = report
		}
	}
	if latest == nil {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorPolicy, Message: "no matching evaluation report exists for workflow candidate",
		}
	}
	if latest.TenantID != principal.TenantID {
		return workflow.Version{}, core.NewPermissionError("evaluation report belongs to another tenant")
	}
	digest, err := workflow.Digest(candidate)
	if err != nil {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorValidation, Message: "stored workflow candidate is not serializable", Cause: err,
		}
	}
	if latest.WorkflowDigest != digest {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorPolicy, Message: "evaluation report does not match the stored workflow candidate",
		}
	}
	if latest.Gates.Evaluated == 0 {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorPolicy, Message: "latest evaluation report contains no release gates",
		}
	}
	if !latest.Gates.Passed {
		return workflow.Version{}, &core.SkawldError{
			Kind:    core.ErrorPolicy,
			Message: fmt.Sprintf("latest evaluation report %q did not pass release gates", latest.ID),
		}
	}
	if p.maxReportAge > 0 && p.now().Sub(latest.CompletedAt) > p.maxReportAge {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorPolicy, Message: "latest passing evaluation report is stale",
		}
	}
	reviews, err := p.reviews.List(ctx, workflowID, versionNumber)
	if err != nil {
		return workflow.Version{}, err
	}
	var latestReview *workflow.Review
	for index := range reviews {
		review := &reviews[index]
		if review.CandidateDigest != digest {
			continue
		}
		if latestReview == nil || review.ReviewedAt.After(latestReview.ReviewedAt) ||
			review.ReviewedAt.Equal(latestReview.ReviewedAt) && review.ID > latestReview.ID {
			latestReview = review
		}
	}
	if latestReview == nil {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorPolicy, Message: "no human review exists for the exact workflow candidate",
		}
	}
	if latestReview.Decision != workflow.ReviewApproved {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorPolicy, Message: "latest human review did not approve the workflow candidate",
		}
	}
	published, err := p.workflows.Publish(ctx, workflowID, versionNumber, principal)
	if err != nil {
		return workflow.Version{}, err
	}
	if p.audit != nil {
		if err := p.audit.Append(ctx, audit.Event{
			ID: id.New(), Type: audit.EventWorkflowPublished, Timestamp: p.now(),
			TenantID: principal.TenantID, ActorID: principal.ActorID,
			WorkflowID: workflowID, WorkflowVersion: versionNumber, Outcome: "published",
			Attributes: map[string]interface{}{
				"review_id": latestReview.ID, "evaluation_report_id": latest.ID,
				"candidate_digest": digest,
			},
		}); err != nil {
			return published, &core.SkawldError{
				Kind:    core.ErrorWorkflow,
				Message: "workflow was published but its audit event failed",
				Cause:   err,
			}
		}
	}
	return published, nil
}
