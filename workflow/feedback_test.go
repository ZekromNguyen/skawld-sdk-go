package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestExecutionFeedbackIsImmutableAndTenantScoped(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer-a"}
	ctx := core.WithPrincipal(context.Background(), principal)
	execution := terminalFeedbackExecution(principal)
	feedback, err := NewExecutionFeedback(
		execution,
		FeedbackRequest{
			Disposition: FeedbackCorrection, StepID: "validate",
			ReasonCode: "wrong.account", Comment: "Use the payable account.",
			CorrectedAction: "select_payable_account",
		},
		principal,
		time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryFeedbackStore()
	if err := store.Save(ctx, feedback); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, feedback); !errors.Is(
		err, &core.SkawldError{Kind: core.ErrorConflict},
	) {
		t.Fatalf("duplicate feedback error = %v", err)
	}
	items, err := store.List(ctx, FeedbackFilter{
		WorkflowID: "invoice", Disposition: FeedbackCorrection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].CorrectedAction != "select_payable_account" {
		t.Fatalf("unexpected feedback list: %+v", items)
	}
	otherTenant := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-b", ActorID: "reviewer-b",
	})
	if _, exists, err := store.Get(
		otherTenant, feedback.ID,
	); err != nil || exists {
		t.Fatalf("cross-tenant feedback leaked: exists=%t err=%v", exists, err)
	}
}

func TestNewExecutionFeedbackRejectsUnsafeShapesAndRunningExecutions(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer-a"}
	execution := terminalFeedbackExecution(principal)
	execution.Status = ExecutionRunning
	if _, err := NewExecutionFeedback(
		execution,
		FeedbackRequest{Disposition: FeedbackAccepted, ReasonCode: "accepted"},
		principal, time.Now(),
	); !errors.Is(err, &core.SkawldError{Kind: core.ErrorConflict}) {
		t.Fatalf("running execution feedback error = %v", err)
	}
	execution.Status = ExecutionCompleted
	if _, err := NewExecutionFeedback(
		execution,
		FeedbackRequest{
			Disposition: FeedbackCorrection, StepID: "missing",
			ReasonCode: "wrong.step", CorrectedAction: "correct_action",
		},
		principal, time.Now(),
	); !errors.Is(err, &core.SkawldError{Kind: core.ErrorValidation}) {
		t.Fatalf("unknown step feedback error = %v", err)
	}
	if _, err := NewExecutionFeedback(
		execution,
		FeedbackRequest{
			Disposition: FeedbackCorrection, StepID: "validate",
			ReasonCode: "wrong.step", CorrectedAction: "run(); rm -rf",
		},
		principal, time.Now(),
	); err == nil {
		t.Fatal("expected executable-looking corrected action to fail identifier validation")
	}
}

func terminalFeedbackExecution(principal core.Principal) Execution {
	now := time.Now().UTC()
	return Execution{
		ID: "execution-1", WorkflowID: "invoice", WorkflowVersion: 2,
		Principal: principal, Status: ExecutionCompleted,
		Steps: []StepExecution{{
			StepID: "validate", Status: StepCompleted,
		}},
		StartedAt: now, CompletedAt: now,
	}
}
