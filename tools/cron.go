package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// ─── Cron Job Registry ────────────────────────────────────────────────────

// CronJob represents a scheduled prompt that fires on a cron schedule.
type CronJob struct {
	ID         string
	Expression string
	Prompt     string
	NextFire   time.Time
	cancel     context.CancelFunc
}

type CronManager struct {
	mu     sync.RWMutex
	jobs   map[string]*CronJob
	seq    int
	ctx    context.Context
	cancel context.CancelFunc
}

func NewCronManager() *CronManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &CronManager{jobs: make(map[string]*CronJob), ctx: ctx, cancel: cancel}
}

// ─── CronCreate Tool ──────────────────────────────────────────────────────

type CronCreateTool struct {
	Manager *CronManager
	once    sync.Once
}

type CronListTool struct {
	Manager *CronManager
	once    sync.Once
}

type CronDeleteTool struct {
	Manager *CronManager
	once    sync.Once
}

func NewCronTools() (*CronCreateTool, *CronListTool, *CronDeleteTool) {
	manager := NewCronManager()
	return &CronCreateTool{Manager: manager}, &CronListTool{Manager: manager}, &CronDeleteTool{Manager: manager}
}

func (t *CronCreateTool) manager() *CronManager {
	t.once.Do(func() {
		if t.Manager == nil {
			t.Manager = NewCronManager()
		}
	})
	return t.Manager
}
func (t *CronListTool) manager() *CronManager {
	t.once.Do(func() {
		if t.Manager == nil {
			t.Manager = NewCronManager()
		}
	})
	return t.Manager
}
func (t *CronDeleteTool) manager() *CronManager {
	t.once.Do(func() {
		if t.Manager == nil {
			t.Manager = NewCronManager()
		}
	})
	return t.Manager
}

func (*CronCreateTool) Name() string { return "CronCreate" }
func (*CronCreateTool) Description() string {
	return "Schedule a prompt to fire at a regular interval using a 5-field cron expression (minute hour day-of-month month day-of-week)."
}
func (*CronCreateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"cron":   map[string]interface{}{"type": "string", "description": "5-field cron expression: 'min hr dom mon dow'. Supports *, */N, comma lists, ranges."},
			"prompt": map[string]interface{}{"type": "string", "description": "Prompt text to enqueue when the cron fires."},
		},
		"required": []string{"cron", "prompt"},
	}
}
func (*CronCreateTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (*CronCreateTool) ParallelSafe() bool    { return true }
func (t *CronCreateTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseCronCreateInput(raw)
	if err != nil {
		return nil, err
	}
	if _, err := parseCronFields(parsed.Expression); err != nil {
		return nil, core.NewToolExecutionError(t.Name(), err.Error())
	}
	return parsed.mapValue(), nil
}
func (t *CronCreateTool) Summarize(input map[string]interface{}) string {
	in := cronCreateInputFrom(input)
	return fmt.Sprintf("Cron job: %s", truncate(in.Prompt, 60))
}
func (t *CronCreateTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := cronCreateInputFrom(input)
	manager := t.manager()

	fields, err := parseCronFields(in.Expression)
	if err != nil {
		return core.ToolResult{Content: "CronCreate error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}

	manager.mu.Lock()
	manager.seq++
	id := fmt.Sprintf("cron-%d", manager.seq)

	sessionID := ctx.SessionID
	store := ctx.SessionStore
	runCtx, cancel := context.WithCancel(manager.ctx)
	now := time.Now()
	nextFire := nextCronTime(fields, now)

	job := &CronJob{
		ID:         id,
		Expression: in.Expression,
		Prompt:     in.Prompt,
		NextFire:   nextFire,
		cancel:     cancel,
	}
	manager.jobs[id] = job
	manager.mu.Unlock()

	go manager.runCronLoop(runCtx, id, in.Prompt, fields, nextFire, sessionID, store, ctx.Principal)

	var nextStr string
	if nextFire.IsZero() {
		nextStr = "no future match"
	} else {
		nextStr = nextFire.Format("2006-01-02 15:04")
	}
	return core.ToolResult{
		Content: fmt.Sprintf("Cron job created:\n  ID: %s\n  Expression: %s\n  Prompt: %s\n  Next fire: %s", id, in.Expression, in.Prompt, nextStr),
		Summary: fmt.Sprintf("Cron job %s created", id),
	}, nil
}

// ─── CronList Tool ────────────────────────────────────────────────────────

func (*CronListTool) Name() string { return "CronList" }
func (*CronListTool) Description() string {
	return "List all scheduled cron jobs with their IDs, expressions, prompts, and next-fire times."
}
func (*CronListTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}
func (*CronListTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (*CronListTool) ParallelSafe() bool    { return true }
func (t *CronListTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (t *CronListTool) Summarize(input map[string]interface{}) string { return "Cron job list" }
func (t *CronListTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	manager := t.manager()
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	if len(manager.jobs) == 0 {
		return core.ToolResult{Content: "No scheduled cron jobs.", Summary: "0 cron job(s)"}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-12s %-22s %s\n", "ID", "EXPRESSION", "PROMPT"))
	b.WriteString(strings.Repeat("-", 80) + "\n")
	for _, j := range manager.jobs {
		nextFire := j.NextFire.Format("2006-01-02 15:04")
		b.WriteString(fmt.Sprintf("%-12s %-22s %s (next: %s)\n", j.ID, j.Expression, truncate(j.Prompt, 40), nextFire))
	}
	return core.ToolResult{Content: b.String(), Summary: fmt.Sprintf("%d cron job(s)", len(manager.jobs))}, nil
}

// ─── CronDelete Tool ──────────────────────────────────────────────────────

func (*CronDeleteTool) Name() string { return "CronDelete" }
func (*CronDeleteTool) Description() string {
	return "Delete a scheduled cron job by ID. The job will not fire after deletion."
}
func (*CronDeleteTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{"type": "string", "description": "Cron job ID (e.g., cron-1)."},
		},
		"required": []string{"id"},
	}
}
func (*CronDeleteTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (*CronDeleteTool) ParallelSafe() bool    { return true }
func (t *CronDeleteTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseCronDeleteInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t *CronDeleteTool) Summarize(input map[string]interface{}) string {
	in := cronDeleteInputFrom(input)
	return fmt.Sprintf("Cron delete %s", in.ID)
}
func (t *CronDeleteTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := cronDeleteInputFrom(input)
	manager := t.manager()

	manager.mu.Lock()
	job, ok := manager.jobs[in.ID]
	if ok {
		delete(manager.jobs, in.ID)
	}
	manager.mu.Unlock()

	if !ok {
		return core.ToolResult{Content: fmt.Sprintf("Cron job %q not found.", in.ID), Summary: t.Summarize(input), IsError: true}, nil
	}

	job.cancel()
	return core.ToolResult{Content: fmt.Sprintf("Cron job %q deleted.", in.ID), Summary: t.Summarize(input)}, nil
}

// ─── Cron Loop ────────────────────────────────────────────────────────────

func (m *CronManager) runCronLoop(ctx context.Context, id, prompt string, fields cronFields, nextFire time.Time, sessionID string, store core.SessionStore, principal core.Principal) {
	if nextFire.IsZero() {
		return
	}

	for {
		dur := time.Until(nextFire)
		if dur < 0 {
			dur = 0
		}

		timer := time.NewTimer(dur)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		// Check if still registered
		m.mu.RLock()
		_, stillExists := m.jobs[id]
		m.mu.RUnlock()
		if !stillExists {
			return
		}

		// Fire: append message to session
		if store != nil && sessionID != "" {
			msg := core.Message{
				Role: "user",
				Content: []core.ContentBlock{
					{Type: core.BlockText, Text: prompt},
				},
			}
			storeCtx := core.WithPrincipal(ctx, principal)
			if _, err := store.AppendMessages(storeCtx, sessionID, []core.Message{msg}); err != nil {
				// If we can't append, the store might be gone — stop
				return
			}
		}

		// Compute next fire time
		next := nextCronTime(fields, time.Now())

		m.mu.Lock()
		if job, ok := m.jobs[id]; ok {
			job.NextFire = next
		}
		m.mu.Unlock()

		if next.IsZero() {
			return
		}
		nextFire = next
	}
}

func (m *CronManager) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()
	m.mu.Lock()
	for id, job := range m.jobs {
		job.cancel()
		delete(m.jobs, id)
	}
	m.mu.Unlock()
	return nil
}

func (t *CronCreateTool) Close() error { return t.manager().Close() }
func (t *CronListTool) Close() error   { return t.manager().Close() }
func (t *CronDeleteTool) Close() error { return t.manager().Close() }

// ─── Cron Parser ──────────────────────────────────────────────────────────

type cronField struct {
	values map[int]bool // explicit values
	all    bool         // * wildcard
	step   int          // */N step, only meaningful with all=true
}

type cronFields struct {
	minute cronField
	hour   cronField
	dom    cronField // day of month
	month  cronField
	dow    cronField // day of week (0=Sunday)
}

func parseCronFields(expression string) (cronFields, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return cronFields{}, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}

	parsers := []struct {
		name string
		min  int
		max  int
		raw  string
		out  *cronField
	}{
		{"minute", 0, 59, fields[0], nil},
		{"hour", 0, 23, fields[1], nil},
		{"day-of-month", 1, 31, fields[2], nil},
		{"month", 1, 12, fields[3], nil},
		{"day-of-week", 0, 6, fields[4], nil},
	}

	var result cronFields
	targets := []*cronField{&result.minute, &result.hour, &result.dom, &result.month, &result.dow}
	for i, p := range parsers {
		p.out = targets[i]
		cf, err := parseCronField(p.raw, p.min, p.max)
		if err != nil {
			return result, fmt.Errorf("%s: %w", p.name, err)
		}
		*targets[i] = cf
	}
	return result, nil
}

func parseCronField(raw string, min, max int) (cronField, error) {
	if raw == "*" {
		return cronField{all: true}, nil
	}
	if strings.HasPrefix(raw, "*/") {
		stepStr := raw[2:]
		step, err := strconv.Atoi(stepStr)
		if err != nil || step < 1 {
			return cronField{}, fmt.Errorf("invalid step %q in %q", stepStr, raw)
		}
		return cronField{all: true, step: step}, nil
	}

	values := make(map[int]bool)
	parts := strings.Split(raw, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "*" {
			return cronField{all: true}, nil
		}
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			lo, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return cronField{}, fmt.Errorf("invalid range start in %q", part)
			}
			hi, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return cronField{}, fmt.Errorf("invalid range end in %q", part)
			}
			if lo < min || hi > max || lo > hi {
				return cronField{}, fmt.Errorf("range %d-%d out of bounds [%d,%d]", lo, hi, min, max)
			}
			for v := lo; v <= hi; v++ {
				values[v] = true
			}
		} else {
			v, err := strconv.Atoi(part)
			if err != nil {
				return cronField{}, fmt.Errorf("invalid value %q", part)
			}
			if v < min || v > max {
				return cronField{}, fmt.Errorf("value %d out of bounds [%d,%d]", v, min, max)
			}
			values[v] = true
		}
	}
	return cronField{values: values}, nil
}

// ─── Next Fire Time Calculator ────────────────────────────────────────────

func nextCronTime(fields cronFields, after time.Time) time.Time {
	// Start from the next minute to avoid immediate re-fire
	t := after.Truncate(time.Minute).Add(time.Minute)

	// Search up to 2 years ahead
	deadline := after.Add(2 * 365 * 24 * time.Hour)
	maxMinutes := int(deadline.Sub(t) / time.Minute)
	if maxMinutes > 2*366*24*60 {
		maxMinutes = 2 * 366 * 24 * 60
	}

	for i := 0; i < maxMinutes; i++ {
		if matchCronTime(fields, t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{} // no match within 2 years
}

func matchCronTime(fields cronFields, t time.Time) bool {
	return matchField(fields.minute, int(t.Minute())) &&
		matchField(fields.hour, int(t.Hour())) &&
		matchField(fields.dom, t.Day()) &&
		matchField(fields.month, int(t.Month())) &&
		matchField(fields.dow, int(t.Weekday()))
}

func matchField(cf cronField, value int) bool {
	if cf.all {
		if cf.step > 0 {
			return value%cf.step == 0
		}
		return true
	}
	return cf.values[value]
}

// ─── Cron Expression Format ──────────────────────────────────────────────

// FormatCronDescription returns a human-readable description of a cron expression.
func FormatCronDescription(expression string) string {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return expression
	}
	return fmt.Sprintf("At minute %s of hour %s on day-of-month %s in month %s (dow: %s)",
		fields[0], fields[1], fields[2], fields[3], fields[4])
}
