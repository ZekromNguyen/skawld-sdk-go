package sessions

import (
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestInMemoryTaskEdgesAreReciprocalAndRemovable(t *testing.T) {
	store := NewInMemoryStore()
	a, err := store.CreateTask("s", core.CreateTaskInput{Subject: "a", Description: ""})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateTask("s", core.CreateTaskInput{Subject: "b", Description: ""})
	if err != nil {
		t.Fatal(err)
	}

	updated, ok, err := store.UpdateTask("s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task to be updated")
	}
	if len(updated.Blocks) != 1 || updated.Blocks[0] != b.ID {
		t.Fatalf("expected %s to block %s, got %+v", a.ID, b.ID, updated.Blocks)
	}
	blocked, ok, err := store.GetTask("s", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(blocked.BlockedBy) != 1 || blocked.BlockedBy[0] != a.ID {
		t.Fatalf("expected %s to be blocked by %s, got %+v", b.ID, a.ID, blocked.BlockedBy)
	}

	updated, ok, err = store.UpdateTask("s", a.ID, core.TaskPatch{RemoveBlocks: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task to be updated")
	}
	if len(updated.Blocks) != 0 {
		t.Fatalf("expected blocks to be removed, got %+v", updated.Blocks)
	}
	blocked, _, err = store.GetTask("s", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.BlockedBy) != 0 {
		t.Fatalf("expected reciprocal blocked_by to be removed, got %+v", blocked.BlockedBy)
	}
}

func TestInMemoryTaskCycleIsRejectedWithoutPartialCommit(t *testing.T) {
	store := NewInMemoryStore()
	a, _ := store.CreateTask("s", core.CreateTaskInput{Subject: "a", Description: ""})
	b, _ := store.CreateTask("s", core.CreateTaskInput{Subject: "b", Description: ""})
	c, _ := store.CreateTask("s", core.CreateTaskInput{Subject: "c", Description: ""})

	if _, _, err := store.UpdateTask("s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateTask("s", b.ID, core.TaskPatch{AddBlocks: []string{c.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateTask("s", c.ID, core.TaskPatch{AddBlocks: []string{a.ID}}); err == nil {
		t.Fatal("expected cycle to be rejected")
	}

	got, _, err := store.GetTask("s", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 0 {
		t.Fatalf("expected rejected update not to commit, got %+v", got.Blocks)
	}
	aTask, _, err := store.GetTask("s", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aTask.BlockedBy) != 0 {
		t.Fatalf("expected rejected reciprocal update not to commit, got %+v", aTask.BlockedBy)
	}
}

func TestInMemoryTaskMetadataNullDeletesKeys(t *testing.T) {
	store := NewInMemoryStore()
	task, err := store.CreateTask("s", core.CreateTaskInput{
		Subject:     "task",
		Description: "",
		Metadata:    map[string]interface{}{"keep": "yes", "drop": "old"},
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, ok, err := store.UpdateTask("s", task.ID, core.TaskPatch{
		Metadata: map[string]interface{}{"drop": nil, "new": "value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task to be updated")
	}
	if _, exists := updated.Metadata["drop"]; exists {
		t.Fatalf("expected drop metadata key to be deleted, got %+v", updated.Metadata)
	}
	if updated.Metadata["keep"] != "yes" || updated.Metadata["new"] != "value" {
		t.Fatalf("unexpected metadata after patch: %+v", updated.Metadata)
	}
}

func TestInMemoryTaskDeletedStatusDetachesEdges(t *testing.T) {
	store := NewInMemoryStore()
	a, _ := store.CreateTask("s", core.CreateTaskInput{Subject: "a", Description: ""})
	b, _ := store.CreateTask("s", core.CreateTaskInput{Subject: "b", Description: ""})
	if _, _, err := store.UpdateTask("s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}

	deleted, ok, err := store.UpdateTask("s", a.ID, core.TaskPatch{Status: taskStatusPtr(core.TaskDeleted)})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task to be updated")
	}
	if deleted.Status != core.TaskDeleted {
		t.Fatalf("expected deleted status, got %s", deleted.Status)
	}
	if len(deleted.Blocks) != 0 || len(deleted.BlockedBy) != 0 {
		t.Fatalf("expected deleted task to detach edges, got %+v / %+v", deleted.Blocks, deleted.BlockedBy)
	}
	blocked, _, err := store.GetTask("s", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.BlockedBy) != 0 {
		t.Fatalf("expected reciprocal edge to be detached, got %+v", blocked.BlockedBy)
	}
}

func taskStatusPtr(status core.TaskStatus) *core.TaskStatus {
	return &status
}
