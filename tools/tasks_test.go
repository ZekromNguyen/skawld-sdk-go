package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
)

func TestTaskUpdateToolSupportsEdgesMetadataAndDeletedStatus(t *testing.T) {
	store := sessions.NewInMemoryStore()
	ctx := context.Background()
	a, err := store.CreateTask(ctx, "s", core.CreateTaskInput{
		Subject:     "a",
		Description: "",
		Metadata:    map[string]interface{}{"drop": "old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "b", Description: ""})
	if err != nil {
		t.Fatal(err)
	}
	toolCtx := core.ToolContext{Context: ctx, SessionID: "s", SessionStore: store}
	tool := TaskUpdateTool{}
	input, err := tool.Validate(map[string]interface{}{
		"id":         a.ID,
		"add_blocks": []interface{}{b.ID},
		"metadata":   map[string]interface{}{"drop": nil, "new": "value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(input, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected update to succeed, got %v", res.Content)
	}
	updated, _, err := store.GetTask(ctx, "s", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Blocks) != 1 || updated.Blocks[0] != b.ID {
		t.Fatalf("expected task to block %s, got %+v", b.ID, updated.Blocks)
	}
	if _, exists := updated.Metadata["drop"]; exists {
		t.Fatalf("expected null metadata value to delete key, got %+v", updated.Metadata)
	}

	input, err = tool.Validate(map[string]interface{}{"id": a.ID, "status": "deleted"})
	if err != nil {
		t.Fatal(err)
	}
	res, err = tool.Execute(input, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected delete-compatible status update to succeed, got %v", res.Content)
	}
	deleted, _, err := store.GetTask(ctx, "s", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != core.TaskDeleted {
		t.Fatalf("expected deleted status, got %s", deleted.Status)
	}
}

func TestTaskUpdateToolRejectsInvalidPatchInputs(t *testing.T) {
	tool := TaskUpdateTool{}
	if _, err := tool.Validate(map[string]interface{}{"id": "1", "status": "blocked"}); err == nil {
		t.Fatal("expected invalid status to fail validation")
	}
	if _, err := tool.Validate(map[string]interface{}{"id": "1", "add_blocks": []interface{}{"2", 3}}); err == nil {
		t.Fatal("expected non-string dependency id to fail validation")
	}
	if _, err := tool.Validate(map[string]interface{}{"id": "1", "metadata": "bad"}); err == nil {
		t.Fatal("expected invalid metadata to fail validation")
	}
}

func TestTaskUpdateToolReportsCycleErrors(t *testing.T) {
	store := sessions.NewInMemoryStore()
	ctx := context.Background()
	a, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "a", Description: ""})
	b, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "b", Description: ""})
	toolCtx := core.ToolContext{Context: ctx, SessionID: "s", SessionStore: store}
	tool := TaskUpdateTool{}
	input, err := tool.Validate(map[string]interface{}{"id": a.ID, "add_blocks": []interface{}{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := tool.Execute(input, toolCtx); err != nil || res.IsError {
		t.Fatalf("expected first edge update to succeed: %v %v", res.Content, err)
	}

	input, err = tool.Validate(map[string]interface{}{"id": b.ID, "add_blocks": []interface{}{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(input, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content.(string), "cycle") {
		t.Fatalf("expected cycle error result, got %+v", res)
	}
}
