package evaluation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type publisherCatalog string

func (c publisherCatalog) ToolCatalogFingerprint(
	context.Context,
	[]string,
) (string, error) {
	return string(c), nil
}

func TestPublisherPublishesCandidateWithLatestPassingGatedReport(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "release-reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	workflows := workflow.NewMemoryStore()
	candidate := evaluationCandidate(principal.TenantID)
	if _, err := workflows.SaveCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	reports := NewMemoryStore()
	suite := passingCandidateSuite()
	report, err := NewRunner(RunnerOptions{Store: reports}).Run(ctx, suite, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gates.Passed || report.Gates.Evaluated != 1 {
		t.Fatalf("candidate evaluation did not pass: %+v", report.Gates)
	}
	reviews := workflow.NewMemoryReviewStore()
	saveApprovedReview(t, ctx, reviews, candidate, principal)
	publisher, err := NewPublisher(PublisherOptions{
		Workflows: workflows, Reports: reports, Reviews: reviews, RequiredSuite: suite.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := publisher.Publish(ctx, candidate.Workflow.ID, candidate.Version, principal)
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != workflow.VersionPublished || published.PublishedBy != principal.ActorID {
		t.Fatalf("unexpected published candidate: %+v", published)
	}
}

func TestPublisherRejectsLatestFailingReport(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "release-reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	workflows := workflow.NewMemoryStore()
	candidate := evaluationCandidate(principal.TenantID)
	if _, err := workflows.SaveCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	reports := NewMemoryStore()
	if _, err := NewRunner(RunnerOptions{Store: reports}).Run(ctx, passingCandidateSuite(), candidate); err != nil {
		t.Fatal(err)
	}
	failingSuite := passingCandidateSuite()
	failingSuite.Scenarios[0].Expected.Status = workflow.ExecutionFailed
	future := time.Now().Add(time.Hour)
	failingReport, err := NewRunner(RunnerOptions{
		Store: reports, Now: func() time.Time { return future },
	}).Run(ctx, failingSuite, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if failingReport.Gates.Passed {
		t.Fatal("expected latest evaluation gates to fail")
	}
	publisher, err := NewPublisher(PublisherOptions{
		Workflows: workflows, Reports: reports, Reviews: workflow.NewMemoryReviewStore(),
		RequiredSuite: failingSuite.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = publisher.Publish(ctx, candidate.Workflow.ID, candidate.Version, principal)
	if err == nil {
		t.Fatal("expected publication to be blocked")
	}
	var skawldErr *core.SkawldError
	if !errors.As(err, &skawldErr) || skawldErr.Kind != core.ErrorPolicy {
		t.Fatalf("expected policy error, got %T %v", err, err)
	}
}

func TestPublisherRejectsToolContractDrift(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "release-reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	workflows := workflow.NewMemoryStore()
	candidate := workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow: workflow.Workflow{
			ID: "tool-candidate", TenantID: principal.TenantID, Name: "Tool candidate",
		},
		Version: 1, Status: workflow.VersionCandidate, CreatedAt: time.Now(),
		ToolCatalogDigest: "compiled-catalog",
		Steps: []workflow.Step{{
			ID: "lookup", Kind: workflow.StepTool,
			Tool: &workflow.ToolCall{Name: "erp.lookup"},
		}},
	}
	if _, err := workflows.SaveCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	reports := NewMemoryStore()
	suite := Suite{
		Name: "tool-release",
		Scenarios: []Scenario{{
			ID: "lookup",
			Tools: map[string]ToolFixture{
				"erp.lookup": {
					Descriptor: core.ToolDescriptor{
						Risk: core.RiskLow, SideEffect: core.SideEffectNone,
						Idempotency: core.IdempotencyNotApplicable,
					},
				},
			},
			Expected: ExpectedOutcome{
				Status:    workflow.ExecutionCompleted,
				ToolCalls: []ExpectedToolCall{{Name: "erp.lookup"}},
			},
		}},
		Gates: []Gate{{
			Metric: MetricTaskSuccessRate, Operator: GateAtLeast, Value: 1,
		}},
	}
	if _, err := NewRunner(RunnerOptions{Store: reports}).Run(ctx, suite, candidate); err != nil {
		t.Fatal(err)
	}
	reviews := workflow.NewMemoryReviewStore()
	saveApprovedReview(t, ctx, reviews, candidate, principal)
	publisher, err := NewPublisher(PublisherOptions{
		Workflows: workflows, Reports: reports, Reviews: reviews,
		ToolCatalog: publisherCatalog("changed-catalog"), RequiredSuite: suite.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(
		ctx, candidate.Workflow.ID, candidate.Version, principal,
	); !errors.Is(err, &core.SkawldError{Kind: core.ErrorPolicy}) {
		t.Fatalf("catalog drift error = %v", err)
	}
}

func TestPublisherRejectsReportForDifferentCandidateDocument(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "release-reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	workflows := workflow.NewMemoryStore()
	candidate := evaluationCandidate(principal.TenantID)
	if _, err := workflows.SaveCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	reports := NewMemoryStore()
	differentDocument := candidate
	differentDocument.Workflow.Description = "not the stored candidate"
	if _, err := NewRunner(RunnerOptions{Store: reports}).Run(
		ctx, passingCandidateSuite(), differentDocument,
	); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewPublisher(PublisherOptions{
		Workflows: workflows, Reports: reports, Reviews: workflow.NewMemoryReviewStore(),
		RequiredSuite: "candidate-release",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = publisher.Publish(ctx, candidate.Workflow.ID, candidate.Version, principal)
	if err == nil {
		t.Fatal("expected mismatched candidate digest to block publication")
	}
	var skawldErr *core.SkawldError
	if !errors.As(err, &skawldErr) || skawldErr.Kind != core.ErrorPolicy {
		t.Fatalf("expected policy error, got %T %v", err, err)
	}
}

func TestPublisherRequiresLatestExactCandidateReviewApproval(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "release-reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	workflows := workflow.NewMemoryStore()
	candidate := evaluationCandidate(principal.TenantID)
	if _, err := workflows.SaveCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	reports := NewMemoryStore()
	suite := passingCandidateSuite()
	if _, err := NewRunner(RunnerOptions{Store: reports}).Run(ctx, suite, candidate); err != nil {
		t.Fatal(err)
	}
	reviews := workflow.NewMemoryReviewStore()
	rejected, err := workflow.NewReview(
		candidate, workflow.ReviewRejected, principal, "unsafe mapping", time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reviews.Save(ctx, rejected); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewPublisher(PublisherOptions{
		Workflows: workflows, Reports: reports, Reviews: reviews, RequiredSuite: suite.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(ctx, candidate.Workflow.ID, candidate.Version, principal); err == nil {
		t.Fatal("expected rejected review to block publication")
	}
	saveApprovedReview(t, ctx, reviews, candidate, principal)
	if _, err := publisher.Publish(ctx, candidate.Workflow.ID, candidate.Version, principal); err != nil {
		t.Fatal(err)
	}
}

func saveApprovedReview(
	t *testing.T,
	ctx context.Context,
	store workflow.ReviewStore,
	candidate workflow.Version,
	principal core.Principal,
) {
	t.Helper()
	review, err := workflow.NewReview(
		candidate, workflow.ReviewApproved, principal, "reviewed candidate", time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, review); err != nil {
		t.Fatal(err)
	}
}

func evaluationCandidate(tenantID string) workflow.Version {
	return workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow:      workflow.Workflow{ID: "candidate", TenantID: tenantID, Name: "Candidate"},
		Version:       1, Status: workflow.VersionCandidate, CreatedAt: time.Now(),
		Steps: []workflow.Step{{
			ID: "validate", Kind: workflow.StepValidation,
			Validation: &workflow.Validation{Condition: workflow.Condition{
				Left: workflow.Value{Literal: true}, Operator: workflow.OpEqual,
				Right: workflow.Value{Literal: true},
			}},
		}},
	}
}

func passingCandidateSuite() Suite {
	return Suite{
		Name: "candidate-release",
		Scenarios: []Scenario{{
			ID: "valid", Expected: ExpectedOutcome{Status: workflow.ExecutionCompleted},
		}},
		Gates: []Gate{{
			Metric: MetricTaskSuccessRate, Operator: GateAtLeast, Value: 1,
		}},
	}
}
