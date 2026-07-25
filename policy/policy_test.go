package policy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestRiskPolicyRequiresApprovalForUncertainBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		descriptor core.ToolDescriptor
	}{
		{
			name: "unknown side effect",
			descriptor: core.ToolDescriptor{
				Risk: core.RiskMedium, SideEffect: core.SideEffectUnknown,
				Idempotency: core.IdempotencyUnsupported,
			},
		},
		{
			name: "network access",
			descriptor: core.ToolDescriptor{
				Risk: core.RiskLow, SideEffect: core.SideEffectNone,
				Idempotency: core.IdempotencyNotApplicable, NetworkAccess: true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := (RiskPolicy{}).Evaluate(context.Background(), Action{Descriptor: test.descriptor})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Kind != RequireApproval {
				t.Fatalf("decision = %q, want %q", decision.Kind, RequireApproval)
			}
		})
	}
}

func TestApprovalLifecycleExpiresAndCancelsPendingApprovals(t *testing.T) {
	store := NewMemoryApprovalStore()
	now := time.Unix(100, 0).UTC()
	store.now = func() time.Time { return now }
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	expiring, err := store.Request(ctx, Approval{
		ExecutionID: "exec-1", StepID: "post", Summary: "Post",
		Risk: core.RiskHigh, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Expire(
		ctx, expiring.ID, principal, "too early",
	); err == nil {
		t.Fatal("expected early expiration to fail")
	}
	now = now.Add(2 * time.Minute)
	expired, err := store.Expire(
		ctx, expiring.ID, principal, "deadline elapsed",
	)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status != ApprovalExpired {
		t.Fatalf("expired status = %q", expired.Status)
	}
	canceling, err := store.Request(ctx, Approval{
		ExecutionID: "exec-2", StepID: "post", Summary: "Post",
		Risk: core.RiskHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := store.Cancel(
		ctx, canceling.ID, principal, "workflow canceled",
	)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != ApprovalCanceled {
		t.Fatalf("canceled status = %q", canceled.Status)
	}
	pending, err := store.List(ctx, ApprovalFilter{Status: ApprovalPending})
	if err != nil || len(pending) != 0 {
		t.Fatalf("unexpected pending approvals: %+v err=%v", pending, err)
	}
}

func TestRolePolicyRequiresEveryCapabilityBeforeRiskEvaluation(t *testing.T) {
	authorization, err := NewRolePolicy(RolePolicyOptions{
		RoleCapabilities: map[string][]string{
			"accountant": {"invoice.read", "invoice.write"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	action := Action{
		Principal: core.Principal{
			TenantID: "tenant-a", ActorID: "actor-a", Roles: []string{"accountant"},
		},
		Descriptor: core.ToolDescriptor{
			Risk: core.RiskLow, SideEffect: core.SideEffectNone,
			Permissions: []string{"invoice.read", "invoice.write"},
		},
	}
	decision, err := authorization.Evaluate(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Kind != Allow {
		t.Fatalf("authorized decision = %+v, want allow", decision)
	}

	action.Descriptor.Permissions = append(
		action.Descriptor.Permissions, "payments.execute",
	)
	decision, err = authorization.Evaluate(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Kind != Deny ||
		!strings.Contains(decision.Reason, "payments.execute") {
		t.Fatalf("missing capability decision = %+v, want deny", decision)
	}

	action.Principal.ActorID = ""
	decision, err = authorization.Evaluate(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Kind != Deny {
		t.Fatalf("partial identity decision = %+v, want deny", decision)
	}
}
