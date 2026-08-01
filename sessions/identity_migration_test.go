package sessions

import (
	"context"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestMigrateLegacySessionIdentity(t *testing.T) {
	store := NewInMemoryStore()
	record, err := store.Create(
		context.Background(), "legacy-session",
		map[string]interface{}{"source": "legacy"},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	updated, err := MigrateLegacySessionIdentity(
		core.WithPrincipal(context.Background(), principal),
		store, record.ID, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !core.CanAccessSessionStrict(principal, updated.Meta) {
		t.Fatalf("strict identity was not persisted: %+v", updated.Meta)
	}
	other := core.Principal{
		TenantID: "tenant-b", ActorID: "actor-b",
	}
	if _, err := MigrateLegacySessionIdentity(
		core.WithPrincipal(context.Background(), other),
		store, record.ID, other,
	); err == nil {
		t.Fatal("expected conflicting ownership rejection")
	}
}
