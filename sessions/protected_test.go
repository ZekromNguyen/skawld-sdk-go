package sessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	sessionssqlite "github.com/ZekromNguyen/skawld-sdk-go/sessions/sqlite"
	"github.com/ZekromNguyen/skawld-sdk-go/storage"
)

func TestProtectedStoreEncryptsSessionPayloads(t *testing.T) {
	inner := NewInMemoryStore()
	store := newProtectedTestStore(t, inner)
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	ctx := core.WithPrincipal(context.Background(), principal)

	record, err := store.Create(ctx, "session-a", map[string]interface{}{
		"account": "secret-account",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Meta["account"] != "secret-account" {
		t.Fatalf("decoded metadata was not returned: %+v", record.Meta)
	}
	message := core.Message{
		Role: "user",
		Content: []core.ContentBlock{
			core.Text("secret-message"),
		},
	}
	if _, err := store.AppendMessages(
		ctx, record.ID, []core.Message{message},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInvokedSkills(
		ctx, record.ID, []core.InvokedSkillRecord{{
			Name: "secret-skill", SubstitutedBody: "secret-body",
			InvokedAt: 10,
		}},
	); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, record.ID, core.CreateTaskInput{
		Subject: "secret-subject", Description: "secret-description",
		Metadata: map[string]interface{}{"token": "secret-token"},
	})
	if err != nil {
		t.Fatal(err)
	}

	rawRecord, exists, err := inner.Load(ctx, record.ID)
	if err != nil || !exists {
		t.Fatalf("load raw record: exists=%v err=%v", exists, err)
	}
	rawMessages, err := inner.LoadMessages(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	rawTask, exists, err := inner.GetTask(ctx, record.ID, task.ID)
	if err != nil || !exists {
		t.Fatalf("load raw task: exists=%v err=%v", exists, err)
	}
	raw, err := json.Marshal([]interface{}{
		rawRecord, rawMessages, rawTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"secret-account", "secret-message", "secret-skill",
		"secret-body", "secret-subject", "secret-description",
		"secret-token",
	} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("plaintext %q reached the underlying store: %s", secret, raw)
		}
	}

	loaded, exists, err := store.Load(ctx, record.ID)
	if err != nil || !exists {
		t.Fatalf("load protected record: exists=%v err=%v", exists, err)
	}
	if loaded.InvokedSkills[0].Name != "secret-skill" ||
		loaded.InvokedSkills[0].SubstitutedBody != "secret-body" {
		t.Fatalf("invoked skill did not round trip: %+v", loaded)
	}
	messages, err := store.LoadMessages(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 ||
		messages[0].Message.Content[0].Text != "secret-message" {
		t.Fatalf("message did not round trip: %+v", messages)
	}
	loadedTask, exists, err := store.GetTask(ctx, record.ID, task.ID)
	if err != nil || !exists {
		t.Fatalf("get protected task: exists=%v err=%v", exists, err)
	}
	if loadedTask.Subject != "secret-subject" ||
		loadedTask.Metadata["token"] != "secret-token" {
		t.Fatalf("task did not round trip: %+v", loadedTask)
	}
	tasks, err := store.ListTasks(ctx, record.ID)
	if err != nil || len(tasks) != 1 ||
		tasks[0].Description != "secret-description" {
		t.Fatalf("protected task listing failed: %+v err=%v", tasks, err)
	}
	updated, err := store.UpdateMeta(
		ctx, record.ID,
		map[string]interface{}{"account": "rotated-secret"},
	)
	if err != nil || updated.Meta["account"] != "rotated-secret" {
		t.Fatalf("protected metadata update failed: %+v err=%v", updated, err)
	}
}

func TestProtectedStoreEnforcesActorIsolationAndFilteredListing(t *testing.T) {
	inner := NewInMemoryStore()
	store := newProtectedTestStore(t, inner)
	actorA := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	actorB := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-b",
	}
	ctxA := core.WithPrincipal(context.Background(), actorA)
	ctxB := core.WithPrincipal(context.Background(), actorB)
	if _, err := store.Create(
		ctxA, "session-a", map[string]interface{}{"private": "a"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(
		ctxB, "session-b", map[string]interface{}{"private": "b"},
	); err != nil {
		t.Fatal(err)
	}

	if _, exists, err := store.Load(ctxB, "session-a"); err == nil || exists {
		t.Fatalf("cross-actor load was not denied: exists=%v err=%v", exists, err)
	}
	records, err := store.List(ctxA, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "session-a" {
		t.Fatalf("list crossed actor boundary: %+v", records)
	}
	if err := store.Delete(ctxB, "session-a"); err == nil {
		t.Fatal("cross-actor delete was not denied")
	}
	if _, err := store.List(context.Background(), 10, 0); err == nil {
		t.Fatal("unauthenticated listing was not denied")
	}
	if err := store.Delete(ctxA, "session-a"); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := store.Load(ctxA, "session-a"); err != nil || exists {
		t.Fatalf("deleted protected session still exists: %v %v", exists, err)
	}
}

func TestProtectedStoreRejectsTamperedOwnershipHeader(t *testing.T) {
	inner := NewInMemoryStore()
	store := newProtectedTestStore(t, inner)
	actorA := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	actorB := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-b",
	}
	ctxA := core.WithPrincipal(context.Background(), actorA)
	ctxB := core.WithPrincipal(context.Background(), actorB)
	if _, err := store.Create(
		ctxA, "session-a", map[string]interface{}{"private": "secret"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.UpdateMeta(
		ctxA, "session-a",
		map[string]interface{}{core.SessionMetaActorID: actorB.ActorID},
	); err != nil {
		t.Fatal(err)
	}

	if _, exists, err := store.Load(ctxB, "session-a"); err == nil || exists {
		t.Fatalf(
			"tampered ownership header was accepted: exists=%v err=%v",
			exists, err,
		)
	}
}

func TestProtectedStoreRejectsCrossSessionPayloadSubstitution(t *testing.T) {
	inner := NewInMemoryStore()
	store := newProtectedTestStore(t, inner)
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	for _, sessionID := range []string{
		"session-a", "session-b", "session-c",
	} {
		if _, err := store.Create(
			ctx, sessionID,
			map[string]interface{}{"private": sessionID},
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AppendMessages(
		ctx, "session-a",
		[]core.Message{{
			Role: "user",
			Content: []core.ContentBlock{
				core.Text("session-a secret"),
			},
		}},
	); err != nil {
		t.Fatal(err)
	}

	rawA, exists, err := inner.Load(ctx, "session-a")
	if err != nil || !exists {
		t.Fatalf("load raw session a: exists=%v err=%v", exists, err)
	}
	if _, err := inner.UpdateMeta(
		ctx, "session-b",
		map[string]interface{}{
			protectedMetaKey: rawA.Meta[protectedMetaKey],
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := store.Load(
		ctx, "session-b",
	); err == nil || exists {
		t.Fatalf(
			"cross-session metadata substitution was accepted: exists=%v err=%v",
			exists, err,
		)
	}

	rawMessages, err := inner.LoadMessages(ctx, "session-a")
	if err != nil || len(rawMessages) != 1 {
		t.Fatalf("load raw messages: messages=%d err=%v", len(rawMessages), err)
	}
	if _, err := inner.AppendMessages(
		ctx, "session-c",
		[]core.Message{rawMessages[0].Message},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadMessages(ctx, "session-c"); err == nil {
		t.Fatal("cross-session message substitution was accepted")
	}
}

func TestProtectedStoreUpdatesEncryptedTaskPayload(t *testing.T) {
	inner := NewInMemoryStore()
	store := newProtectedTestStore(t, inner)
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	if _, err := store.Create(ctx, "session-a", nil); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(
		ctx, "session-a",
		core.CreateTaskInput{
			Subject: "before", Description: "description",
			Metadata: map[string]interface{}{"keep": "yes"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	after := "after"
	owner := "operator"
	task, exists, err := store.UpdateTask(
		ctx, "session-a", task.ID,
		core.TaskPatch{
			Subject: &after, Owner: &owner,
			Metadata: map[string]interface{}{
				"keep": nil, "new": "value",
			},
		},
	)
	if err != nil || !exists {
		t.Fatalf("update task: exists=%v err=%v", exists, err)
	}
	if task.Subject != after || task.Owner != owner ||
		task.Metadata["new"] != "value" ||
		task.Metadata["keep"] != nil {
		t.Fatalf("task update did not round trip: %+v", task)
	}
	raw, _, err := inner.GetTask(ctx, "session-a", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(raw)
	if strings.Contains(string(encoded), after) ||
		strings.Contains(string(encoded), owner) ||
		strings.Contains(string(encoded), "value") {
		t.Fatalf("updated task leaked plaintext: %s", encoded)
	}
	deleted, err := store.DeleteTask(ctx, "session-a", task.ID)
	if err != nil || !deleted {
		t.Fatalf("delete protected task: deleted=%v err=%v", deleted, err)
	}
}

func TestProtectedStoreSurvivesSQLiteRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	raw, err := sessionssqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := storage.NewStaticKeyProvider(
		map[string]storage.EncryptionKey{
			"tenant-a": {
				ID: "key-a", Bytes: []byte("0123456789abcdef0123456789abcdef"),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := storage.NewAESGCMProtector(keys)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProtectedStore(raw, protector)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Durable() {
		t.Fatal("protected SQLite store was not marked durable")
	}
	principal := core.Principal{
		TenantID: "tenant-a", ActorID: "actor-a",
	}
	ctx := core.WithPrincipal(context.Background(), principal)
	if _, err := store.Create(
		ctx, "session-a", map[string]interface{}{"secret": "metadata"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessages(
		ctx, "session-a",
		[]core.Message{{
			Role: "user",
			Content: []core.ContentBlock{
				core.Text("persistent secret"),
			},
		}},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	databaseBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(databaseBytes), "persistent secret") ||
		strings.Contains(string(databaseBytes), `"secret":"metadata"`) {
		t.Fatal("protected SQLite database contains plaintext session data")
	}

	raw, err = sessionssqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewProtectedStore(raw, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	record, exists, err := reopened.Load(ctx, "session-a")
	if err != nil || !exists ||
		record.Meta["secret"] != "metadata" {
		t.Fatalf("reopen protected record: %+v exists=%v err=%v", record, exists, err)
	}
	messages, err := reopened.LoadMessages(ctx, "session-a")
	if err != nil || len(messages) != 1 ||
		messages[0].Message.Content[0].Text != "persistent secret" {
		t.Fatalf("reopen protected messages: %+v err=%v", messages, err)
	}
}

func newProtectedTestStore(
	t *testing.T,
	inner core.SessionStore,
) *ProtectedStore {
	t.Helper()
	keys, err := storage.NewStaticKeyProvider(
		map[string]storage.EncryptionKey{
			"tenant-a": {
				ID: "key-a", Bytes: []byte("0123456789abcdef0123456789abcdef"),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := storage.NewAESGCMProtector(keys)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := NewProtectedStore(inner, protector)
	if err != nil {
		t.Fatal(err)
	}
	return protected
}
