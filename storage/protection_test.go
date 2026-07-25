package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestAESGCMProtectorBindsCiphertextToTenant(t *testing.T) {
	keys, err := NewStaticKeyProvider(map[string]EncryptionKey{
		"tenant-a": {
			ID: "key-a", Bytes: bytes.Repeat([]byte{1}, 32),
		},
		"tenant-b": {
			ID: "key-b", Bytes: bytes.Repeat([]byte{2}, 32),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewAESGCMProtector(keys)
	if err != nil {
		t.Fatal(err)
	}
	principalA := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	protected, err := protector.Protect(
		core.WithPrincipal(context.Background(), principalA),
		[]byte(`{"secret":"invoice"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, []byte("invoice")) {
		t.Fatalf("ciphertext contains plaintext: %s", protected)
	}
	plaintext, err := protector.Unprotect(
		core.WithPrincipal(context.Background(), principalA), protected,
	)
	if err != nil || string(plaintext) != `{"secret":"invoice"}` {
		t.Fatalf("plaintext=%s err=%v", plaintext, err)
	}
	principalB := core.Principal{
		TenantID: "tenant-b", ActorID: "actor-b",
	}
	if _, err := protector.Unprotect(
		core.WithPrincipal(context.Background(), principalB), protected,
	); !errors.Is(err, &core.SkawldError{
		Kind: core.ErrorPermissionDenied,
	}) {
		t.Fatalf("cross-tenant decrypt error=%v", err)
	}
}

func TestStaticKeyProviderRotationRetainsHistoricalKeys(t *testing.T) {
	keys, err := NewStaticKeyProvider(map[string]EncryptionKey{
		"tenant-a": {ID: "v1", Bytes: bytes.Repeat([]byte{1}, 32)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithPrincipal(context.Background(), core.Principal{
		TenantID: "tenant-a", ActorID: "operator",
	})
	protector, _ := NewAESGCMProtector(keys)
	oldDocument, err := protector.Protect(ctx, []byte(`{"value":"old"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.Rotate("tenant-a", EncryptionKey{
		ID: "v2", Bytes: bytes.Repeat([]byte{2}, 32),
	}); err != nil {
		t.Fatal(err)
	}
	if err := keys.Rotate("tenant-a", EncryptionKey{
		ID: "v2", Bytes: bytes.Repeat([]byte{3}, 32),
	}); err == nil {
		t.Fatal("key id was reused with different key material")
	}
	newDocument, err := protector.Protect(ctx, []byte(`{"value":"new"}`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldDocument, newDocument) {
		t.Fatal("rotation did not produce a new envelope")
	}
	if _, err := protector.Unprotect(ctx, oldDocument); err != nil {
		t.Fatalf("historical key unavailable after rotation: %v", err)
	}
	if err := keys.Retire("tenant-a", "v2"); err == nil {
		t.Fatal("current key retirement should fail")
	}
	if err := keys.Retire("tenant-a", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := protector.Unprotect(ctx, oldDocument); err == nil {
		t.Fatal("retired historical key still decrypted a document")
	}
}
