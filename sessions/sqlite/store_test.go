package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	skawld "github.com/skawld/skawld-sdk-go"
	"github.com/skawld/skawld-sdk-go/core"
	_ "modernc.org/sqlite"
)

func TestStorePersistsSessionsMessagesTasksAndSkillsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store := openTestStore(t, path)
	ctx := context.Background()

	rec, err := store.Create(ctx, "session-1", map[string]interface{}{"project": "sdk"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "session-1" {
		t.Fatalf("unexpected session id: %s", rec.ID)
	}
	if _, err := store.AppendMessages(ctx, "session-1", []core.Message{
		{Role: "user", Content: []core.ContentBlock{core.Text("hello")}},
		{
			Role: "assistant",
			Content: []core.ContentBlock{
				core.Text("done"),
				core.ToolUse("call-1", "TaskList", map[string]interface{}{}),
			},
			ProviderMetadata: core.MessageProviderMetadata{
				OpenAIResponses: &core.OpenAIResponsesMetadata{ResponseID: "resp_1", OutputItems: []map[string]interface{}{{"type": "message"}}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateMeta(ctx, "session-1", map[string]interface{}{"branch": "main"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInvokedSkills(ctx, "session-1", []core.InvokedSkillRecord{
		{Name: "review", SubstitutedBody: "Review this code", InvokedAt: 10},
	}); err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateTask(ctx, "session-1", core.CreateTaskInput{
		Subject:     "a",
		Description: "first",
		Metadata:    map[string]interface{}{"drop": "old", "keep": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateTask(ctx, "session-1", core.CreateTaskInput{Subject: "b", Description: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.UpdateTask(ctx, "session-1", a.ID, core.TaskPatch{
		AddBlocks: []string{b.ID},
		Metadata:  map[string]interface{}{"drop": nil, "new": "value"},
	}); err != nil || !ok {
		t.Fatalf("expected task update to succeed, ok=%v err=%v", ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestStore(t, path)
	defer closeTestStore(t, reopened)
	loaded, ok, err := reopened.Load(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected session to load after reopen")
	}
	if loaded.Meta["project"] != "sdk" || loaded.Meta["branch"] != "main" {
		t.Fatalf("unexpected metadata after reopen: %+v", loaded.Meta)
	}
	if len(loaded.InvokedSkills) != 1 || loaded.InvokedSkills[0].Name != "review" {
		t.Fatalf("unexpected invoked skills after reopen: %+v", loaded.InvokedSkills)
	}
	messages, err := reopened.LoadMessages(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Seq != 1 || messages[1].Seq != 2 {
		t.Fatalf("unexpected stored messages: %+v", messages)
	}
	if messages[1].Message.ProviderMetadata.OpenAIResponses == nil || messages[1].Message.ProviderMetadata.OpenAIResponses.ResponseID != "resp_1" {
		t.Fatalf("provider metadata was not preserved: %+v", messages[1].Message.ProviderMetadata)
	}
	tasks, err := reopened.ListTasks(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != "1" || tasks[1].ID != "2" {
		t.Fatalf("unexpected task ordering: %+v", tasks)
	}
	first := tasks[0]
	if len(first.Blocks) != 1 || first.Blocks[0] != b.ID {
		t.Fatalf("expected task edge to persist, got %+v", first.Blocks)
	}
	if _, exists := first.Metadata["drop"]; exists {
		t.Fatalf("expected null metadata patch to delete key, got %+v", first.Metadata)
	}
	if first.Metadata["keep"] != "yes" || first.Metadata["new"] != "value" {
		t.Fatalf("unexpected task metadata: %+v", first.Metadata)
	}
	second, ok, err := reopened.GetTask(ctx, "session-1", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(second.BlockedBy) != 1 || second.BlockedBy[0] != a.ID {
		t.Fatalf("expected reciprocal edge to persist, got ok=%v task=%+v", ok, second)
	}
}

func TestStoreResumesAgentMessagesAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	provider := &resumeProvider{}
	store := openTestStore(t, path)
	agent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider:     provider,
		Model:        "fake-model",
		SessionStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := agent.Session(context.Background(), skawld.SessionOptions{ID: "resume-session"})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Run(context.Background(), "first", skawld.RunOptions{}) {
	}
	if err := agent.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestStore(t, path)
	resumedAgent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider:     provider,
		Model:        "fake-model",
		SessionStore: reopened,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resumedAgent.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	resumed, err := resumedAgent.Session(context.Background(), skawld.SessionOptions{ID: "resume-session"})
	if err != nil {
		t.Fatal(err)
	}
	for range resumed.Run(context.Background(), "second", skawld.RunOptions{}) {
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(provider.requests))
	}
	var found bool
	for _, msg := range provider.requests[1].Messages {
		if msg.ProviderMetadata.OpenAIResponses != nil && msg.ProviderMetadata.OpenAIResponses.ResponseID == "resp_sqlite" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected provider metadata from reopened SQLite session in next request")
	}
}

func TestStoreTaskCycleRejectedWithoutPartialCommit(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions.db"))
	defer closeTestStore(t, store)
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
	cTask, _, err := store.GetTask(ctx, "s", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cTask.Blocks) != 0 {
		t.Fatalf("expected rejected update not to commit, got %+v", cTask.Blocks)
	}
}

func TestStoreDeletedStatusAndDeleteCascade(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	if _, err := store.Create(ctx, "s", nil); err != nil {
		t.Fatal(err)
	}
	a, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "a", Description: ""})
	b, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "b", Description: ""})
	if _, _, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}
	deleted, ok, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{Status: taskStatusPtr(core.TaskDeleted)})
	if err != nil || !ok {
		t.Fatalf("expected delete-compatible update, ok=%v err=%v", ok, err)
	}
	if deleted.Status != core.TaskDeleted || len(deleted.Blocks) != 0 {
		t.Fatalf("expected deleted task to detach edges, got %+v", deleted)
	}
	other, _, err := store.GetTask(ctx, "s", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.BlockedBy) != 0 {
		t.Fatalf("expected reciprocal edge to be detached, got %+v", other.BlockedBy)
	}
	if err := store.Delete(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Load(ctx, "s"); err != nil || ok {
		t.Fatalf("expected session delete, ok=%v err=%v", ok, err)
	}
	tasks, err := store.ListTasks(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected tasks to cascade delete, got %+v", tasks)
	}
}

func TestStoreListOrdersByUpdatedAtAndSupportsPaging(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	if _, err := store.Create(ctx, "old", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "new", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessages(ctx, "old", []core.Message{{Role: "user", Content: []core.ContentBlock{core.Text("touch")}}}); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "old" {
		t.Fatalf("expected touched session first, got %+v", listed)
	}
	listed, err = store.List(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "new" {
		t.Fatalf("expected second page to contain new session, got %+v", listed)
	}
}

func TestStoreOperationsHonorCanceledContext(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions.db"))
	defer closeTestStore(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Create(ctx, "s", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled create, got %v", err)
	}
	if _, err := store.List(ctx, 10, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled list, got %v", err)
	}
	if _, err := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "task", Description: ""}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled create task, got %v", err)
	}
}

func TestStoreLockedOperationCanBeCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store := openTestStore(t, path)
	defer closeTestStore(t, store)
	ctx := context.Background()
	if _, err := store.Create(ctx, "s", nil); err != nil {
		t.Fatal(err)
	}

	lockDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer lockDB.Close()
	if _, err := lockDB.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatal(err)
	}
	tx, err := lockDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = updated_at WHERE id = ?`, "s"); err != nil {
		t.Fatal(err)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = store.UpdateMeta(shortCtx, "s", map[string]interface{}{"locked": true})
	if !errors.Is(err, context.DeadlineExceeded) && err == nil {
		t.Fatalf("expected locked update to honor context deadline, got %v", err)
	}
}

func TestStoreTaskUpdateTouchesOnlyTargetRowsAndEdges(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	a, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "a", Description: ""})
	b, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "b", Description: ""})
	c, _ := store.CreateTask(ctx, "s", core.CreateTaskInput{Subject: "c", Description: ""})
	if _, _, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{AddBlocks: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}

	bBefore := taskUpdatedAt(t, store, "s", b.ID)
	cBefore := taskUpdatedAt(t, store, "s", c.ID)
	edgesBefore := taskEdgeRows(t, store, "s")
	subject := "a updated"
	if _, ok, err := store.UpdateTask(ctx, "s", a.ID, core.TaskPatch{Subject: &subject}); err != nil || !ok {
		t.Fatalf("expected targeted task update, ok=%v err=%v", ok, err)
	}
	if got := taskUpdatedAt(t, store, "s", b.ID); got != bBefore {
		t.Fatalf("unrelated blocked task row was rewritten: before=%s after=%s", bBefore, got)
	}
	if got := taskUpdatedAt(t, store, "s", c.ID); got != cBefore {
		t.Fatalf("unrelated task row was rewritten: before=%s after=%s", cBefore, got)
	}
	if got := taskEdgeRows(t, store, "s"); got != edgesBefore {
		t.Fatalf("unchanged edge set was rewritten: before=%d after=%d", edgesBefore, got)
	}
}

func taskUpdatedAt(t *testing.T, store *Store, sessionID, taskID string) string {
	t.Helper()
	var updated string
	if err := store.db.QueryRowContext(context.Background(), `SELECT updated_at FROM tasks WHERE session_id = ? AND id = ?`, sessionID, taskID).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	return updated
}

func taskEdgeRows(t *testing.T, store *Store, sessionID string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM task_edges WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func closeTestStore(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func taskStatusPtr(status core.TaskStatus) *core.TaskStatus {
	return &status
}

type resumeProvider struct {
	requests []core.ProviderRequest
}

func (p *resumeProvider) ID() string { return "resume" }
func (p *resumeProvider) ContextWindow(model core.ModelID) int {
	return 100000
}
func (p *resumeProvider) Stream(ctx context.Context, req core.ProviderRequest) (<-chan core.ProviderStreamEvent, <-chan error) {
	out := make(chan core.ProviderStreamEvent)
	errs := make(chan error, 1)
	p.requests = append(p.requests, req)
	call := len(p.requests)
	go func() {
		defer close(out)
		defer close(errs)
		out <- core.ProviderStreamEvent{Type: "message_start", Model: req.Model}
		out <- core.ProviderStreamEvent{Type: "text_delta", Text: "ok"}
		meta := core.MessageProviderMetadata{}
		if call == 1 {
			meta.OpenAIResponses = &core.OpenAIResponsesMetadata{ResponseID: "resp_sqlite"}
		}
		out <- core.ProviderStreamEvent{Type: "message_end", StopReason: core.StopEndTurn, ProviderMetadata: meta}
	}()
	return out, errs
}
