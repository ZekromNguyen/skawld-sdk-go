package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestMemoryReviewStoreIsImmutableTenantIsolatedAndDigestBound(t *testing.T) {
	principal := core.Principal{TenantID: "tenant-a", ActorID: "reviewer"}
	ctx := core.WithPrincipal(context.Background(), principal)
	candidate := Version{
		SchemaVersion: SchemaVersion,
		Workflow:      Workflow{ID: "invoice", TenantID: principal.TenantID, Name: "Invoice"},
		Version:       1, Status: VersionCandidate, CreatedAt: time.Now().UTC(),
		Steps: []Step{{
			ID: "approve", Kind: StepApproval,
			Approval: &ApprovalSpec{Summary: "Approve invoice", Risk: core.RiskHigh},
		}},
	}
	review, err := NewReview(candidate, ReviewApproved, principal, "checked", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryReviewStore()
	if err := store.Save(ctx, review); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, review); err == nil {
		t.Fatal("expected duplicate immutable review to fail")
	}
	loaded, ok, err := store.Get(ctx, review.ID)
	if err != nil || !ok || loaded.CandidateDigest != review.CandidateDigest {
		t.Fatalf("load review: ok=%t review=%+v err=%v", ok, loaded, err)
	}
	changed := candidate
	changed.Workflow.Description = "changed"
	changedDigest, err := Digest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == review.CandidateDigest {
		t.Fatal("candidate digest did not bind the reviewed document")
	}
	other := core.WithPrincipal(
		context.Background(), core.Principal{TenantID: "tenant-b", ActorID: "reviewer"},
	)
	if _, _, err := store.Get(other, review.ID); err == nil {
		t.Fatal("expected cross-tenant review read to fail")
	}
}
