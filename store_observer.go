package skawld

import (
	"context"
	"fmt"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

type observedSessionStore struct {
	inner core.SessionStore
	agent *Agent
}

func (s *observedSessionStore) Create(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	start := time.Now()
	rec, err := s.inner.Create(ctx, id, meta)
	sessionID := rec.ID
	if sessionID == "" {
		sessionID = id
	}
	s.observe(ctx, "session.create", sessionID, start, err)
	if err != nil {
		return core.SessionRecord{}, fmt.Errorf("store create session: %w", err)
	}
	return rec, nil
}

func (s *observedSessionStore) Load(ctx context.Context, id string) (core.SessionRecord, bool, error) {
	start := time.Now()
	rec, ok, err := s.inner.Load(ctx, id)
	s.observe(ctx, "session.load", id, start, err)
	if err != nil {
		return core.SessionRecord{}, false, fmt.Errorf("store load session %q: %w", id, err)
	}
	return rec, ok, nil
}

func (s *observedSessionStore) LoadMessages(ctx context.Context, id string) ([]core.StoredMessage, error) {
	start := time.Now()
	messages, err := s.inner.LoadMessages(ctx, id)
	s.observe(ctx, "messages.load", id, start, err)
	if err != nil {
		return nil, fmt.Errorf("store load messages for session %q: %w", id, err)
	}
	return messages, nil
}

func (s *observedSessionStore) AppendMessages(ctx context.Context, id string, messages []core.Message) ([]core.StoredMessage, error) {
	start := time.Now()
	stored, err := s.inner.AppendMessages(ctx, id, messages)
	s.observe(ctx, "messages.append", id, start, err)
	if err != nil {
		return nil, fmt.Errorf("store append messages for session %q: %w", id, err)
	}
	return stored, nil
}

func (s *observedSessionStore) UpdateMeta(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	start := time.Now()
	rec, err := s.inner.UpdateMeta(ctx, id, meta)
	s.observe(ctx, "session.update_meta", id, start, err)
	if err != nil {
		return core.SessionRecord{}, fmt.Errorf("store update session metadata %q: %w", id, err)
	}
	return rec, nil
}

func (s *observedSessionStore) SetInvokedSkills(ctx context.Context, id string, skills []core.InvokedSkillRecord) error {
	start := time.Now()
	err := s.inner.SetInvokedSkills(ctx, id, skills)
	s.observe(ctx, "skills.set_invoked", id, start, err)
	if err != nil {
		return fmt.Errorf("store set invoked skills for session %q: %w", id, err)
	}
	return nil
}

func (s *observedSessionStore) List(ctx context.Context, limit, offset int) ([]core.SessionRecord, error) {
	start := time.Now()
	records, err := s.inner.List(ctx, limit, offset)
	s.observe(ctx, "session.list", "", start, err)
	if err != nil {
		return nil, fmt.Errorf("store list sessions: %w", err)
	}
	return records, nil
}

func (s *observedSessionStore) Delete(ctx context.Context, id string) error {
	start := time.Now()
	err := s.inner.Delete(ctx, id)
	s.observe(ctx, "session.delete", id, start, err)
	if err != nil {
		return fmt.Errorf("store delete session %q: %w", id, err)
	}
	return nil
}

func (s *observedSessionStore) CreateTask(ctx context.Context, sessionID string, input core.CreateTaskInput) (core.Task, error) {
	start := time.Now()
	task, err := s.inner.CreateTask(ctx, sessionID, input)
	s.observe(ctx, "task.create", sessionID, start, err)
	if err != nil {
		return core.Task{}, fmt.Errorf("store create task for session %q: %w", sessionID, err)
	}
	return task, nil
}

func (s *observedSessionStore) GetTask(ctx context.Context, sessionID, taskID string) (core.Task, bool, error) {
	start := time.Now()
	task, ok, err := s.inner.GetTask(ctx, sessionID, taskID)
	s.observe(ctx, "task.get", sessionID, start, err)
	if err != nil {
		return core.Task{}, false, fmt.Errorf("store get task %q for session %q: %w", taskID, sessionID, err)
	}
	return task, ok, nil
}

func (s *observedSessionStore) ListTasks(ctx context.Context, sessionID string) ([]core.Task, error) {
	start := time.Now()
	tasks, err := s.inner.ListTasks(ctx, sessionID)
	s.observe(ctx, "task.list", sessionID, start, err)
	if err != nil {
		return nil, fmt.Errorf("store list tasks for session %q: %w", sessionID, err)
	}
	return tasks, nil
}

func (s *observedSessionStore) UpdateTask(ctx context.Context, sessionID, taskID string, patch core.TaskPatch) (core.Task, bool, error) {
	start := time.Now()
	task, ok, err := s.inner.UpdateTask(ctx, sessionID, taskID, patch)
	s.observe(ctx, "task.update", sessionID, start, err)
	if err != nil {
		return core.Task{}, false, fmt.Errorf("store update task %q for session %q: %w", taskID, sessionID, err)
	}
	return task, ok, nil
}

func (s *observedSessionStore) DeleteTask(ctx context.Context, sessionID, taskID string) (bool, error) {
	start := time.Now()
	ok, err := s.inner.DeleteTask(ctx, sessionID, taskID)
	s.observe(ctx, "task.delete", sessionID, start, err)
	if err != nil {
		return false, fmt.Errorf("store delete task %q for session %q: %w", taskID, sessionID, err)
	}
	return ok, nil
}

func (s *observedSessionStore) Close() error {
	start := time.Now()
	err := s.inner.Close()
	s.observe(context.Background(), "store.close", "", start, err)
	if err != nil {
		return fmt.Errorf("store close: %w", err)
	}
	return nil
}

func (s *observedSessionStore) observe(ctx context.Context, operation, sessionID string, start time.Time, err error) {
	if s.agent == nil {
		return
	}
	s.agent.observe(ctx, core.Observation{
		Type:       core.ObservationStoreOperation,
		Operation:  operation,
		SessionID:  sessionID,
		DurationMS: time.Since(start).Milliseconds(),
		Error:      err,
	})
}
