package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestAuthorizedApprovalStoreRequiresCapabilityAndDistinctApprover(
	t *testing.T,
) {
	requester := core.Principal{
		TenantID: "tenant-a", ActorID: "requester",
		Roles: []string{"operator"},
	}
	approver := core.Principal{
		TenantID: "tenant-a", ActorID: "approver",
		Roles: []string{"finance-approver"},
	}
	authorization, err := NewApprovalRolePolicy(
		ApprovalRolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"finance-approver": {"approval.grant"},
			},
			RequireDistinctApprover: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthorizedApprovalStore(
		NewMemoryApprovalStore(), authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.Request(
		core.WithPrincipal(context.Background(), requester),
		Approval{ExecutionID: "exec-1", StepID: "post"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if approval.RequestedBy != requester.ActorID {
		t.Fatalf("requested_by = %q", approval.RequestedBy)
	}
	if _, err := store.Decide(
		core.WithPrincipal(context.Background(), requester),
		approval.ID, ApprovalGranted, requester, "self approve",
	); !errors.Is(err, &core.SkawldError{
		Kind: core.ErrorPermissionDenied,
	}) {
		t.Fatalf("self approval error = %v", err)
	}
	decided, err := store.Decide(
		core.WithPrincipal(context.Background(), approver),
		approval.ID, ApprovalGranted, approver, "reviewed",
	)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Status != ApprovalGranted ||
		decided.DecidedBy != approver.ActorID {
		t.Fatalf("unexpected approval: %+v", decided)
	}
}

func TestAuthorizedApprovalStoreRejectsMethodRoleInjection(t *testing.T) {
	authorization, err := NewApprovalRolePolicy(
		ApprovalRolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"approver": {"approval.grant"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthorizedApprovalStore(
		NewMemoryApprovalStore(), authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	requester := core.Principal{
		TenantID: "tenant-a", ActorID: "requester",
	}
	approval, err := store.Request(
		core.WithPrincipal(context.Background(), requester),
		Approval{ExecutionID: "exec-1", StepID: "post"},
	)
	if err != nil {
		t.Fatal(err)
	}
	injected := core.Principal{
		TenantID: "tenant-a", ActorID: "actor",
		Roles: []string{"approver"},
	}
	contextPrincipal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor",
		Roles: []string{"viewer"},
	}
	if _, err := store.Decide(
		core.WithPrincipal(context.Background(), contextPrincipal),
		approval.ID, ApprovalGranted, injected, "injected role",
	); !errors.Is(err, &core.SkawldError{
		Kind: core.ErrorPermissionDenied,
	}) {
		t.Fatalf("injected role decision error = %v", err)
	}
}
