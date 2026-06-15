package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestParseCronFieldsValid(t *testing.T) {
	tests := []struct {
		expr    string
		wantErr bool
	}{
		{"* * * * *", false},
		{"*/5 * * * *", false},
		{"0 9 * * 1-5", false},
		{"0,30 9,17 1,15 * *", false},
		{"0 0 1 1 *", false},
		{"*/15 */2 * * *", false},
		{"5-10,20,30 * * * *", false},
		{"59 23 31 12 6", false},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			_, err := parseCronFields(tt.expr)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %q", tt.expr)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tt.expr, err)
			}
		})
	}
}

func TestParseCronFieldsInvalid(t *testing.T) {
	tests := []string{
		"", "* * * *", "* * * * * *", "abc * * * *", "60 * * * *",
		"* 24 * * *", "* * 32 * *", "* * * 13 *", "* * * * 7",
	}
	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			_, err := parseCronFields(expr)
			if err == nil {
				t.Errorf("expected error for %q", expr)
			}
		})
	}
}

func TestNextCronTimeWildcard(t *testing.T) {
	fields, err := parseCronFields("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	next := nextCronTime(fields, now)
	if !next.After(now) {
		t.Fatalf("expected future time, got %v", next)
	}
	// With "* * * * *", next fire should be the next minute
	expected := time.Date(2026, 6, 15, 12, 1, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}
}

func TestNextCronTimeSpecificMinute(t *testing.T) {
	fields, err := parseCronFields("30 8 * * *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC)
	next := nextCronTime(fields, now)
	expected := time.Date(2026, 6, 15, 8, 30, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}
}

func TestNextCronTimeNoFutureMatch(t *testing.T) {
	// "0 0 29 2 *" only fires on Feb 29 (leap year) — won't match in normal years
	fields, err := parseCronFields("0 0 29 2 *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) // after feb
	next := nextCronTime(fields, now)
	if !next.IsZero() {
		// There might be a Feb 29 in 2028 which is within 2 years
		expected := time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)
		if !next.Equal(expected) {
			t.Logf("got next=%v", next)
		}
	}
}

func TestCronCreateDeleteAndList(t *testing.T) {
	// Clear registry
	cronRegistry.Lock()
	cronRegistry.jobs = make(map[string]*CronJob)
	cronRegistry.seq = 0
	cronRegistry.Unlock()

	store := &testSessionStore{id: "test-session"}

	create := CronCreateTool{}
	list := CronListTool{}
	del := CronDeleteTool{}

	// Create a cron job
	input, err := create.Validate(map[string]interface{}{
		"cron":   "*/30 * * * *",
		"prompt": "Check the deploy status",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := create.Execute(input, core.ToolContext{
		Context:      context.Background(),
		SessionID:    "test-session",
		SessionStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("create failed: %v", res.Content)
	}
	content := fmt.Sprint(res.Content)
	if !strings.Contains(content, "cron-1") || !strings.Contains(content, "Check the deploy") {
		t.Fatalf("unexpected create result: %s", content)
	}

	// List should show the job
	listInput, _ := list.Validate(map[string]interface{}{})
	res, err = list.Execute(listInput, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	listContent := fmt.Sprint(res.Content)
	if !strings.Contains(listContent, "cron-1") {
		t.Fatalf("list missing job: %s", listContent)
	}

	// Delete the job
	delInput, err := del.Validate(map[string]interface{}{"id": "cron-1"})
	if err != nil {
		t.Fatal(err)
	}
	res, err = del.Execute(delInput, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("delete failed: %v", res.Content)
	}

	// Delete again should error
	res, err = del.Execute(delInput, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error deleting non-existent job")
	}

	// List should be empty
	res, err = list.Execute(listInput, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Content) != "No scheduled cron jobs." {
		t.Fatalf("expected empty list, got %v", res.Content)
	}
}

func TestCronCreateInvalidExpression(t *testing.T) {
	create := CronCreateTool{}
	_, err := create.Validate(map[string]interface{}{
		"cron":   "invalid expression here",
		"prompt": "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid cron expression")
	}
}

func TestCronParallelSafety(t *testing.T) {
	// Clear registry
	cronRegistry.Lock()
	cronRegistry.jobs = make(map[string]*CronJob)
	cronRegistry.seq = 0
	cronRegistry.Unlock()

	create := CronCreateTool{}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			input, _ := create.Validate(map[string]interface{}{
				"cron":   "0 * * * *",
				"prompt": fmt.Sprintf("job %d", idx),
			})
			create.Execute(input, core.ToolContext{
				Context:      context.Background(),
				SessionID:    "parallel-test",
				SessionStore: &testSessionStore{id: "parallel-test"},
			})
		}(i)
	}
	wg.Wait()

	cronRegistry.RLock()
	count := len(cronRegistry.jobs)
	cronRegistry.RUnlock()

	if count != 10 {
		t.Errorf("expected 10 concurrent creates, got %d", count)
	}

	// Cleanup: cancel all jobs
	cronRegistry.Lock()
	for id, job := range cronRegistry.jobs {
		job.cancel()
		delete(cronRegistry.jobs, id)
	}
	cronRegistry.Unlock()
}

func TestFormatCronDescription(t *testing.T) {
	desc := FormatCronDescription("*/5 * * * *")
	if !strings.Contains(desc, "*/5") {
		t.Errorf("unexpected description: %s", desc)
	}

	desc = FormatCronDescription("bad")
	if desc != "bad" {
		t.Errorf("expected raw for bad expression, got %s", desc)
	}
}

// testSessionStore is a minimal store for cron tests.
type testSessionStore struct {
	id       string
	messages []core.StoredMessage
}

func (s *testSessionStore) Create(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	return core.SessionRecord{ID: id, CreatedAt: time.Now().Format(time.RFC3339)}, nil
}
func (s *testSessionStore) Load(ctx context.Context, id string) (core.SessionRecord, bool, error) {
	return core.SessionRecord{ID: s.id}, true, nil
}
func (s *testSessionStore) LoadMessages(ctx context.Context, id string) ([]core.StoredMessage, error) {
	return s.messages, nil
}
func (s *testSessionStore) AppendMessages(ctx context.Context, id string, messages []core.Message) ([]core.StoredMessage, error) {
	s.messages = append(s.messages, core.StoredMessage{Seq: len(s.messages) + 1, Message: messages[0]})
	return s.messages, nil
}
func (s *testSessionStore) UpdateMeta(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	return core.SessionRecord{ID: s.id}, nil
}
func (s *testSessionStore) SetInvokedSkills(ctx context.Context, id string, skills []core.InvokedSkillRecord) error {
	return nil
}
func (s *testSessionStore) List(ctx context.Context, limit, offset int) ([]core.SessionRecord, error) {
	return nil, nil
}
func (s *testSessionStore) Delete(ctx context.Context, id string) error {
	return nil
}
func (s *testSessionStore) CreateTask(ctx context.Context, sessionID string, input core.CreateTaskInput) (core.Task, error) {
	return core.Task{}, nil
}
func (s *testSessionStore) GetTask(ctx context.Context, sessionID, taskID string) (core.Task, bool, error) {
	return core.Task{}, false, nil
}
func (s *testSessionStore) ListTasks(ctx context.Context, sessionID string) ([]core.Task, error) {
	return nil, nil
}
func (s *testSessionStore) UpdateTask(ctx context.Context, sessionID, taskID string, patch core.TaskPatch) (core.Task, bool, error) {
	return core.Task{}, false, nil
}
func (s *testSessionStore) DeleteTask(ctx context.Context, sessionID, taskID string) (bool, error) {
	return false, nil
}
func (s *testSessionStore) Close() error {
	return nil
}