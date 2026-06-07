package sessions

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
	idgen "github.com/skawld/skawld-sdk-go/internal/id"
)

type InMemoryStore struct {
	mu            sync.Mutex
	sessions      map[string]core.SessionRecord
	messages      map[string][]core.StoredMessage
	invokedSkills map[string][]core.InvokedSkillRecord
	tasks         map[string]map[string]core.Task
	taskCounters  map[string]int
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		sessions:      map[string]core.SessionRecord{},
		messages:      map[string][]core.StoredMessage{},
		invokedSkills: map[string][]core.InvokedSkillRecord{},
		tasks:         map[string]map[string]core.Task{},
		taskCounters:  map[string]int{},
	}
}

func (s *InMemoryStore) Create(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	if err := checkContext(ctx); err != nil {
		return core.SessionRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		id = idgen.New()
	}
	if rec, ok := s.sessions[id]; ok {
		return cloneSessionRecord(rec), nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if meta == nil {
		meta = map[string]interface{}{}
	}
	rec := core.SessionRecord{ID: id, CreatedAt: now, UpdatedAt: now, Meta: cloneMap(meta)}
	s.sessions[id] = cloneSessionRecord(rec)
	return cloneSessionRecord(rec), nil
}

func (s *InMemoryStore) Load(ctx context.Context, id string) (core.SessionRecord, bool, error) {
	if err := checkContext(ctx); err != nil {
		return core.SessionRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok {
		return core.SessionRecord{}, false, nil
	}
	rec.InvokedSkills = append([]core.InvokedSkillRecord(nil), s.invokedSkills[id]...)
	return cloneSessionRecord(rec), true, nil
}

func (s *InMemoryStore) LoadMessages(ctx context.Context, id string) ([]core.StoredMessage, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := cloneStoredMessages(s.messages[id])
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (s *InMemoryStore) AppendMessages(ctx context.Context, id string, messages []core.Message) ([]core.StoredMessage, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	existing := s.messages[id]
	appended := make([]core.StoredMessage, 0, len(messages))
	for i, msg := range messages {
		appended = append(appended, core.StoredMessage{Seq: len(existing) + i + 1, AppendedAt: now, Message: cloneMessage(msg)})
	}
	s.messages[id] = append(existing, cloneStoredMessages(appended)...)
	if rec, ok := s.sessions[id]; ok {
		rec.UpdatedAt = now
		s.sessions[id] = rec
	}
	return cloneStoredMessages(appended), nil
}

func (s *InMemoryStore) UpdateMeta(ctx context.Context, id string, meta map[string]interface{}) (core.SessionRecord, error) {
	if err := checkContext(ctx); err != nil {
		return core.SessionRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok {
		return core.SessionRecord{}, fmt.Errorf("session not found")
	}
	if rec.Meta == nil {
		rec.Meta = map[string]interface{}{}
	}
	for k, v := range meta {
		rec.Meta[k] = cloneJSONValue(v)
	}
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.sessions[id] = cloneSessionRecord(rec)
	return cloneSessionRecord(rec), nil
}

func (s *InMemoryStore) SetInvokedSkills(ctx context.Context, id string, skills []core.InvokedSkillRecord) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invokedSkills[id] = append([]core.InvokedSkillRecord(nil), skills...)
	return nil
}

func (s *InMemoryStore) List(ctx context.Context, limit, offset int) ([]core.SessionRecord, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.SessionRecord, 0, len(s.sessions))
	for _, rec := range s.sessions {
		out = append(out, cloneSessionRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) Delete(ctx context.Context, id string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	delete(s.messages, id)
	delete(s.tasks, id)
	delete(s.taskCounters, id)
	delete(s.invokedSkills, id)
	return nil
}

func (s *InMemoryStore) CreateTask(ctx context.Context, sessionID string, input core.CreateTaskInput) (core.Task, error) {
	if err := checkContext(ctx); err != nil {
		return core.Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.taskCounters[sessionID] + 1
	s.taskCounters[sessionID] = n
	now := time.Now().UTC().Format(time.RFC3339Nano)
	task := core.Task{
		ID: strconv.Itoa(n), SessionID: sessionID, Subject: input.Subject,
		Description: input.Description, ActiveForm: input.ActiveForm,
		Status: core.TaskPending, Blocks: []string{}, BlockedBy: []string{},
		Metadata: cloneMap(input.Metadata), CreatedAt: now, UpdatedAt: now,
	}
	if s.tasks[sessionID] == nil {
		s.tasks[sessionID] = map[string]core.Task{}
	}
	s.tasks[sessionID][task.ID] = cloneTask(task)
	return cloneTask(task), nil
}

func (s *InMemoryStore) GetTask(ctx context.Context, sessionID, taskID string) (core.Task, bool, error) {
	if err := checkContext(ctx); err != nil {
		return core.Task{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[sessionID][taskID]
	return cloneTask(task), ok, nil
}

func (s *InMemoryStore) ListTasks(ctx context.Context, sessionID string) ([]core.Task, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.Task, 0, len(s.tasks[sessionID]))
	for _, task := range s.tasks[sessionID] {
		out = append(out, cloneTask(task))
	}
	sort.Slice(out, func(i, j int) bool {
		ai, _ := strconv.Atoi(out[i].ID)
		aj, _ := strconv.Atoi(out[j].ID)
		return ai < aj
	})
	return out, nil
}

func (s *InMemoryStore) UpdateTask(ctx context.Context, sessionID, taskID string, patch core.TaskPatch) (core.Task, bool, error) {
	if err := checkContext(ctx); err != nil {
		return core.Task{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := cloneTasks(s.tasks[sessionID])
	task, ok := tasks[taskID]
	if !ok {
		return core.Task{}, false, nil
	}
	if patch.Subject != nil {
		task.Subject = *patch.Subject
	}
	if patch.Description != nil {
		task.Description = *patch.Description
	}
	if patch.ActiveForm != nil {
		task.ActiveForm = *patch.ActiveForm
	}
	if patch.Status != nil {
		task.Status = *patch.Status
	}
	if patch.Delete {
		task.Status = core.TaskDeleted
	}
	if patch.Owner != nil {
		task.Owner = *patch.Owner
	}
	if patch.Metadata != nil {
		if task.Metadata == nil {
			task.Metadata = map[string]interface{}{}
		}
		for k, v := range patch.Metadata {
			if v == nil {
				delete(task.Metadata, k)
			} else {
				task.Metadata[k] = cloneJSONValue(v)
			}
		}
		if len(task.Metadata) == 0 {
			task.Metadata = nil
		}
	}
	if task.Status == core.TaskDeleted {
		detachTask(tasks, taskID, &task)
	} else {
		if err := applyTaskEdges(tasks, taskID, &task, patch); err != nil {
			return core.Task{}, true, err
		}
	}
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	tasks[taskID] = task
	s.tasks[sessionID] = tasks
	return cloneTask(task), true, nil
}

func (s *InMemoryStore) DeleteTask(ctx context.Context, sessionID, taskID string) (bool, error) {
	if err := checkContext(ctx); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := cloneTasks(s.tasks[sessionID])
	task, ok := tasks[taskID]
	if !ok {
		return false, nil
	}
	detachTask(tasks, taskID, &task)
	delete(tasks, taskID)
	s.tasks[sessionID] = tasks
	return true, nil
}

func (s *InMemoryStore) Close() error { return nil }

func cloneTasks(tasks map[string]core.Task) map[string]core.Task {
	out := make(map[string]core.Task, len(tasks))
	for id, task := range tasks {
		out[id] = cloneTask(task)
	}
	return out
}

func cloneSessionRecord(rec core.SessionRecord) core.SessionRecord {
	rec.Meta = cloneMap(rec.Meta)
	rec.InvokedSkills = append([]core.InvokedSkillRecord(nil), rec.InvokedSkills...)
	return rec
}

func cloneStoredMessages(messages []core.StoredMessage) []core.StoredMessage {
	out := make([]core.StoredMessage, len(messages))
	for i, msg := range messages {
		out[i] = msg
		out[i].Message = cloneMessage(msg.Message)
	}
	return out
}

func cloneMessage(msg core.Message) core.Message {
	msg.Content = cloneContentBlocks(msg.Content)
	msg.ProviderMetadata = cloneProviderMetadata(msg.ProviderMetadata)
	return msg
}

func cloneProviderMetadata(meta core.MessageProviderMetadata) core.MessageProviderMetadata {
	if meta.OpenAIResponses == nil {
		return meta
	}
	responses := *meta.OpenAIResponses
	responses.OutputItems = cloneMapSlice(responses.OutputItems)
	meta.OpenAIResponses = &responses
	return meta
}

func cloneContentBlocks(blocks []core.ContentBlock) []core.ContentBlock {
	out := make([]core.ContentBlock, len(blocks))
	for i, block := range blocks {
		out[i] = cloneContentBlock(block)
	}
	return out
}

func cloneContentBlock(block core.ContentBlock) core.ContentBlock {
	block.Input = cloneMap(block.Input)
	block.Content = cloneJSONValue(block.Content)
	if nested, ok := block.Content.([]core.ContentBlock); ok {
		block.Content = cloneContentBlocks(nested)
	}
	if nested, ok := block.Content.(core.ContentBlock); ok {
		block.Content = cloneContentBlock(nested)
	}
	if block.Source != nil {
		source := *block.Source
		block.Source = &source
	}
	return block
}

func cloneTask(task core.Task) core.Task {
	task.Blocks = append([]string(nil), task.Blocks...)
	task.BlockedBy = append([]string(nil), task.BlockedBy...)
	task.Metadata = cloneMap(task.Metadata)
	return task
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = cloneJSONValue(v)
	}
	return out
}

func cloneMapSlice(in []map[string]interface{}) []map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make([]map[string]interface{}, len(in))
	for i, item := range in {
		out[i] = cloneMap(item)
	}
	return out
}

func cloneJSONValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneMap(v)
	case []map[string]interface{}:
		return cloneMapSlice(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = cloneJSONValue(v[i])
		}
		return out
	case []core.ContentBlock:
		return cloneContentBlocks(v)
	case core.ContentBlock:
		return cloneContentBlock(v)
	default:
		return v
	}
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func detachTask(tasks map[string]core.Task, taskID string, task *core.Task) {
	for _, id := range task.Blocks {
		if other, ok := tasks[id]; ok {
			other.BlockedBy = removeID(other.BlockedBy, taskID)
			tasks[id] = other
		}
	}
	for _, id := range task.BlockedBy {
		if other, ok := tasks[id]; ok {
			other.Blocks = removeID(other.Blocks, taskID)
			tasks[id] = other
		}
	}
	task.Blocks = []string{}
	task.BlockedBy = []string{}
}

func applyTaskEdges(tasks map[string]core.Task, taskID string, task *core.Task, patch core.TaskPatch) error {
	for _, id := range uniqueIDs(patch.AddBlocks) {
		if id == "" || id == taskID {
			return fmt.Errorf("invalid task dependency: %q", id)
		}
		other, ok := tasks[id]
		if !ok || other.Status == core.TaskDeleted {
			return fmt.Errorf("blocked task not found: %s", id)
		}
		task.Blocks = addID(task.Blocks, id)
		other.BlockedBy = addID(other.BlockedBy, taskID)
		tasks[id] = other
	}
	for _, id := range uniqueIDs(patch.RemoveBlocks) {
		task.Blocks = removeID(task.Blocks, id)
		if other, ok := tasks[id]; ok {
			other.BlockedBy = removeID(other.BlockedBy, taskID)
			tasks[id] = other
		}
	}
	for _, id := range uniqueIDs(patch.AddBlockedBy) {
		if id == "" || id == taskID {
			return fmt.Errorf("invalid task dependency: %q", id)
		}
		other, ok := tasks[id]
		if !ok || other.Status == core.TaskDeleted {
			return fmt.Errorf("blocking task not found: %s", id)
		}
		task.BlockedBy = addID(task.BlockedBy, id)
		other.Blocks = addID(other.Blocks, taskID)
		tasks[id] = other
	}
	for _, id := range uniqueIDs(patch.RemoveBlockedBy) {
		task.BlockedBy = removeID(task.BlockedBy, id)
		if other, ok := tasks[id]; ok {
			other.Blocks = removeID(other.Blocks, taskID)
			tasks[id] = other
		}
	}
	if hasTaskCycle(tasks, taskID, *task) {
		return fmt.Errorf("task dependency cycle detected")
	}
	return nil
}

func uniqueIDs(ids []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func addID(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}

func hasTaskCycle(tasks map[string]core.Task, taskID string, updated core.Task) bool {
	graph := make(map[string][]string, len(tasks))
	for id, task := range tasks {
		if task.Status == core.TaskDeleted {
			continue
		}
		graph[id] = append([]string(nil), task.Blocks...)
	}
	graph[taskID] = append([]string(nil), updated.Blocks...)
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, next := range graph[id] {
			if visit(next) {
				return true
			}
		}
		visiting[id] = false
		visited[id] = true
		return false
	}
	for id := range graph {
		if visit(id) {
			return true
		}
	}
	return false
}
