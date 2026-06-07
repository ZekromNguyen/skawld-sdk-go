package sessions

import (
	"context"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestInMemoryTaskEdgesAreReciprocalAndRemovable(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	a, err := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "a", Description: ""})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "b", Description: ""})
	if err != nil {
		t.Fatal(err)
	}

	updated, ok, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task to be updated")
	}
	if len(updated.Blocks) != 1 || updated.Blocks[0] != b.ID {
		t.Fatalf("expected %s to block %s, got %+v", a.ID, b.ID, updated.Blocks)
	}
	blocked, ok, err := store.GetTask(ctx, "s", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(blocked.BlockedBy) != 1 || blocked.BlockedBy[0] != a.ID {
		t.Fatalf("expected %s to be blocked by %s, got %+v", b.ID, a.ID, blocked.BlockedBy)
	}

	updated, ok, err = store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{RemoveBlocks: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task to be updated")
	}
	if len(updated.Blocks) != 0 {
		t.Fatalf("expected blocks to be removed, got %+v", updated.Blocks)
	}
	blocked, _, err = store.GetTask(ctx, "s", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.BlockedBy) != 0 {
		t.Fatalf("expected reciprocal blocked_by to be removed, got %+v", blocked.BlockedBy)
	}
}

func TestInMemoryTaskCycleIsRejectedWithoutPartialCommit(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	a, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "a", Description: ""})
	b, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "b", Description: ""})
	c, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "c", Description: ""})

	if _, _, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateTask(ctx, "s", b.ID, core.TaskPatch{AddBlocks: []string{c.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateTask(ctx, "s", c.ID, core.TaskPatch{AddBlocks: []string{a.ID}}); err == nil {
		t.Fatal("expected cycle to be rejected")
	}

	got, _, err := store.GetTask(ctx, "s", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 0 {
		t.Fatalf("expected rejected update not to commit, got %+v", got.Blocks)
	}
	aTask, _, err := store.GetTask(ctx, "s", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aTask.BlockedBy) != 0 {
		t.Fatalf("expected rejected reciprocal update not to commit, got %+v", aTask.BlockedBy)
	}
}

func TestInMemoryTaskMetadataNullDeletesKeys(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	task, err := store.CreateTask(ctx, "s", core.CreateTaskInput{
		Subject:     "task",
		Description: "",
		Metadata:    map[string]interface{}{"keep": "yes", "drop": "old"},
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, ok, err := store.UpdateTask(ctx, "s", task.ID, core.TaskPatch{
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
	ctx := context.Background()
	a, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "a", Description: ""})
	b, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "b", Description: ""})
	if _, _, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}

	deleted, ok, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{Status: taskStatusPtr(core.TaskDeleted)})
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
	blocked, _, err := store.GetTask(ctx, "s", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.BlockedBy) != 0 {
		t.Fatalf("expected reciprocal edge to be detached, got %+v", blocked.BlockedBy)
	}
}

func TestInMemoryStoreCopiesMutableBoundaries(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	meta := map[string]interface{}{"nested": map[string]interface{}{"value": "original"}}
	rec, err := store.Create(ctx, "s", meta)
	if err != nil {
		t.Fatal(err)
	}
	rec.Meta["created"] = "mutated"
	meta["nested"].(map[string]interface{})["value"] = "mutated"
	loaded, ok, err := store.Load(ctx, "s")
	if err != nil || !ok {
		t.Fatalf("load failed: ok=%t err=%v", ok, err)
	}
	if _, ok := loaded.Meta["created"]; ok {
		t.Fatalf("returned session metadata mutated store: %+v", loaded.Meta)
	}
	if loaded.Meta["nested"].(map[string]interface{})["value"] != "original" {
		t.Fatalf("input session metadata mutated store: %+v", loaded.Meta)
	}

	msgInput := map[string]interface{}{"nested": map[string]interface{}{"value": "input"}}
	providerItems := []map[string]interface{}{{"id": "item", "nested": map[string]interface{}{"value": "provider"}}}
	stored, err := store.AppendMessages(ctx, "s", []core.Message{{
		Role:    "assistant",
		Content: []core.ContentBlock{core.ToolUse("call", "Tool", msgInput)},
		ProviderMetadata: core.MessageProviderMetadata{
			OpenAIResponses: &core.OpenAIResponsesMetadata{ResponseID: "resp", OutputItems: providerItems},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stored[0].Message.Content[0].Input["added"] = "mutated"
	msgInput["nested"].(map[string]interface{})["value"] = "mutated"
	providerItems[0]["nested"].(map[string]interface{})["value"] = "mutated"
	messages, err := store.LoadMessages(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	gotInput := messages[0].Message.Content[0].Input
	if _, ok := gotInput["added"]; ok {
		t.Fatalf("returned message input mutated store: %+v", gotInput)
	}
	if gotInput["nested"].(map[string]interface{})["value"] != "input" {
		t.Fatalf("input message map mutated store: %+v", gotInput)
	}
	gotProvider := messages[0].Message.ProviderMetadata.OpenAIResponses.OutputItems[0]
	if gotProvider["nested"].(map[string]interface{})["value"] != "provider" {
		t.Fatalf("provider metadata map mutated store: %+v", gotProvider)
	}

	task, err := store.CreateTask(ctx, "s", core.CreateTaskInput{
		Subject:     "task",
		Description: "",
		Metadata:    map[string]interface{}{"nested": map[string]interface{}{"value": "task"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	task.Metadata["added"] = "mutated"
	loadedTask, ok, err := store.GetTask(ctx, "s", task.ID)
	if err != nil || !ok {
		t.Fatalf("task load failed: ok=%t err=%v", ok, err)
	}
	if _, ok := loadedTask.Metadata["added"]; ok {
		t.Fatalf("returned task metadata mutated store: %+v", loadedTask.Metadata)
	}
}

func TestInMemoryStoreHonorsCanceledContext(t *testing.T) {
	store := NewInMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Create(ctx, "s", nil); err == nil {
		t.Fatal("expected canceled context error")
	}
	if _, err := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "task", Description: ""}); err == nil {
		t.Fatal("expected canceled context task error")
	}
}

func taskStatusPtr(status core.TaskStatus) *core.TaskStatus {
	return &status
}
