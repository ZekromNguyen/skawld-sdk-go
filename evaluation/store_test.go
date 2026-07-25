package evaluation

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestMemoryStoreEnforcesTenantIsolation(t *testing.T) {
	store := NewMemoryStore()
	tenantA := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-a"})
	report := Report{
		SchemaVersion: SchemaVersion, ID: "report-1", TenantID: "tenant-a",
		SuiteName: "suite", WorkflowID: "workflow", WorkflowVersion: 1, WorkflowDigest: "fixture-digest",
		StartedAt: time.Now(), CompletedAt: time.Now(),
		Gates: GateResult{Passed: true},
		Cases: []CaseResult{{ScenarioID: "case", Passed: true}},
	}
	if err := store.Save(tenantA, report); err != nil {
		t.Fatal(err)
	}
	tenantB := core.WithPrincipal(context.Background(), core.Principal{TenantID: "tenant-b"})
	if _, _, err := store.Get(tenantB, report.ID); err == nil {
		t.Fatal("cross-tenant report read was not rejected")
	}
	if reports, err := store.List(tenantB, "", 0); err != nil || len(reports) != 0 {
		t.Fatalf("cross-tenant report list leaked data: reports=%+v err=%v", reports, err)
	}
}
