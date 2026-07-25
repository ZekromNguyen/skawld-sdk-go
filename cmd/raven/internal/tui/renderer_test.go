package tui

import (
	"strings"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestChatViewAppendUser(t *testing.T) {
	cv := NewChatView(80, 20, DefaultTheme())
	ev := core.Event{
		Type: core.EventUser,
		Message: core.Message{
			Role: "user",
			Content: []core.ContentBlock{
				{Type: core.BlockText, Text: "Hello, world!"},
			},
		},
	}
	cv.AppendUser(ev)

	if len(cv.Lines) != 2 { // user line + divider
		t.Fatalf("expected 2 lines, got %d", len(cv.Lines))
	}
	if !strings.Contains(stripANSI(cv.Lines[0].Content), "Hello, world!") {
		t.Errorf("user line missing prompt text, got: %s", stripANSI(cv.Lines[0].Content))
	}
}

func TestChatViewAppendAssistant(t *testing.T) {
	cv := NewChatView(80, 20, DefaultTheme())
	ev := core.Event{
		Type: core.EventAssistant,
		Message: core.Message{
			Role: "assistant",
			Content: []core.ContentBlock{
				{Type: core.BlockText, Text: "Sure, I can help with that."},
			},
		},
	}
	cv.AppendAssistant(ev)

	if len(cv.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(cv.Lines))
	}
	plain := stripANSI(cv.Lines[0].Content)
	if !strings.Contains(plain, "Sure, I can help with that.") {
		t.Errorf("assistant line missing text, got: %s", plain)
	}
}

func TestChatViewAppendAssistantWithThinking(t *testing.T) {
	cv := NewChatView(80, 20, DefaultTheme())
	ev := core.Event{
		Type: core.EventAssistant,
		Message: core.Message{
			Role: "assistant",
			Content: []core.ContentBlock{
				{Type: core.BlockThinking, Thinking: "Let me think about this..."},
				{Type: core.BlockText, Text: "Here is the answer."},
			},
		},
	}
	cv.AppendAssistant(ev)

	if len(cv.Lines) != 2 { // thinking line + text line
		t.Fatalf("expected 2 lines (thinking + text), got %d", len(cv.Lines))
	}
}

func TestChatViewAppendToolCalls(t *testing.T) {
	cv := NewChatView(80, 20, DefaultTheme())
	ev := core.Event{
		Type: core.EventAssistant,
		Message: core.Message{
			Role: "assistant",
			Content: []core.ContentBlock{
				{Type: core.BlockText, Text: "Let me read that file."},
				{Type: core.BlockToolUse, Name: "Read", Input: map[string]interface{}{"file_path": "/some/file.go"}},
			},
		},
	}
	cv.AppendAssistant(ev)

	if len(cv.Lines) != 2 { // text + tool line
		t.Fatalf("expected 2 lines (text + tool), got %d", len(cv.Lines))
	}
	plain := stripANSI(cv.Lines[1].Content)
	if !strings.Contains(plain, "Read") {
		t.Errorf("tool line missing tool name, got: %s", plain)
	}
}

func TestChatViewResult(t *testing.T) {
	cv := NewChatView(80, 20, DefaultTheme())
	ev := core.Event{
		Type:       core.EventResult,
		Subtype:    "success",
		DurationMS: 1234,
		TotalUsage: core.Usage{InputTokens: 500, OutputTokens: 200},
	}
	cv.AppendResult(ev)

	if len(cv.Lines) != 1 {
		t.Fatalf("expected 1 result line, got %d", len(cv.Lines))
	}
	plain := stripANSI(cv.Lines[0].Content)
	if !strings.Contains(plain, "Done") {
		t.Errorf("result line missing 'Done', got: %s", plain)
	}
}

func TestStatusViewRender(t *testing.T) {
	sv := NewStatusView(120, DefaultTheme())
	sv.SetModel("claude-sonnet-4-6")
	sv.SetMode("chat")
	sv.Idle = false
	sv.InputTokens = 4200
	sv.ContextWindow = 200000
	sv.Cost = 0.08

	result := sv.Render()
	plain := stripANSI(result)

	if !strings.Contains(plain, "claude-sonnet-4-6") {
		t.Errorf("status bar missing model: %s", plain)
	}
	if !strings.Contains(plain, "chat") {
		t.Errorf("status bar missing mode: %s", plain)
	}
}

func TestStatusViewIdle(t *testing.T) {
	sv := NewStatusView(120, DefaultTheme())
	sv.SetModel("claude-sonnet-4-6")
	sv.SessionID = "4a7f1234"
	sv.MsgCount = 12
	sv.Idle = true

	result := sv.Render()
	plain := stripANSI(result)

	if !strings.Contains(plain, "idle") {
		t.Errorf("status bar missing 'idle' in idle state: %s", plain)
	}
	if !strings.Contains(plain, "4a7f") {
		t.Errorf("status bar missing session ID: %s", plain)
	}
}

func TestProgressBar(t *testing.T) {
	tests := []struct {
		fill float64
		exp  string
	}{
		{0.0, "0%"},
		{0.5, "50%"},
		{1.0, "100%"},
	}

	for _, tt := range tests {
		result := ProgressBar(tt.fill, 20, DefaultTheme())
		plain := stripANSI(result)
		if !strings.Contains(plain, tt.exp) {
			t.Errorf("ProgressBar(%f): expected %q in %q", tt.fill, tt.exp, plain)
		}
	}
}

func TestSummarizeInput(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		exp   string
	}{
		{"Read", map[string]interface{}{"file_path": "/some/long/path/to/file.go"}, "/some/long/path/to/file.go"},
		{"Bash", map[string]interface{}{"command": "go test ./..."}, "go test ./..."},
		{"Write", map[string]interface{}{"file_path": "/tmp/output.txt"}, "/tmp/output.txt"},
		{"Unknown", map[string]interface{}{"foo": "bar"}, ""},
	}

	for _, tt := range tests {
		result := summarizeInput(tt.name, tt.input)
		if !strings.Contains(result, tt.exp) {
			t.Errorf("summarizeInput(%q, %v) = %q, want containing %q", tt.name, tt.input, result, tt.exp)
		}
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"plain text", "plain text"},
		{"\033[1;38;5;81mcolored\033[0m text", "colored text"},
		{"\033[38;2;79;195;247m▓\033[0m", "▓"},
		{"", ""},
	}

	for _, tt := range tests {
		result := stripANSI(tt.input)
		if result != tt.expect {
			t.Errorf("stripANSI(%q) = %q, want %q", tt.input, result, tt.expect)
		}
	}
}

func TestTokenFormat(t *testing.T) {
	tests := []struct {
		n   int
		exp string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.5K"},
		{4200000, "4.2M"},
	}

	for _, tt := range tests {
		result := TokenFormat(tt.n)
		if result != tt.exp {
			t.Errorf("TokenFormat(%d) = %q, want %q", tt.n, result, tt.exp)
		}
	}
}

func TestDurationFormat(t *testing.T) {
	tests := []struct {
		ms  int64
		exp string
	}{
		{45, "45ms"},
		{120, "120ms"},
		{1500, "1.5s"},
		{125000, "2m5s"},
	}

	for _, tt := range tests {
		result := DurationFormat(tt.ms)
		if result != tt.exp {
			t.Errorf("DurationFormat(%d) = %q, want %q", tt.ms, result, tt.exp)
		}
	}
}

func TestCostFormat(t *testing.T) {
	tests := []struct {
		cost float64
		exp  string
	}{
		{0.001, "<$0.01"},
		{0.05, "$0.05"},
		{1.50, "$1.50"},
	}

	for _, tt := range tests {
		result := CostFormat(tt.cost)
		if result != tt.exp {
			t.Errorf("CostFormat(%f) = %q, want %q", tt.cost, result, tt.exp)
		}
	}
}

func TestTruncate(t *testing.T) {
	if r := Truncate("hello", 3); r != "he…" {
		t.Errorf("Truncate: got %q", r)
	}
	if r := Truncate("hi", 10); r != "hi" {
		t.Errorf("Truncate no-op: got %q", r)
	}
}

func TestWelcomeRenderSilhouette(t *testing.T) {
	wv := NewWelcomeView(80, 50, DefaultTheme())
	// Just verify it doesn't panic
	for _, line := range ravenSilhouetteLines {
		result := wv.renderSilhouetteLine(line)
		if result == "" {
			t.Error("renderSilhouetteLine returned empty for non-empty line")
		}
	}
}

func TestRendererHandleEvent(t *testing.T) {
	screen := &Screen{Width: 80, Height: 24, Theme: DefaultTheme(), out: nil}
	r := NewRenderer(screen)

	// Verify NewRenderer initializes views
	if r.Views == nil {
		t.Fatal("renderer views are nil")
	}
	if r.Views.Chat == nil {
		t.Fatal("chat view is nil")
	}
	if r.Views.Status == nil {
		t.Fatal("status view is nil")
	}
	if r.Views.Tools == nil {
		t.Fatal("tools view is nil")
	}
	if r.Views.Welcome == nil {
		t.Fatal("welcome view is nil")
	}

	// Handle a system event
	ev := core.Event{
		Type:      core.EventSystem,
		SessionID: "test-session",
		Model:     "test-model",
	}
	r.HandleEvent(ev)

	if len(r.Views.Chat.Lines) != 1 {
		t.Errorf("expected 1 line after system event, got %d", len(r.Views.Chat.Lines))
	}
}

func TestBufferWrite(t *testing.T) {
	buf := NewBuffer(100, 30)

	buf.SetRow(0, "line 0")
	buf.SetRow(5, "line 5")
	buf.SetRow(29, "line 29")

	if buf.Rows[0] != "line 0" {
		t.Errorf("row 0: got %q", buf.Rows[0])
	}
	if buf.Rows[5] != "line 5" {
		t.Errorf("row 5: got %q", buf.Rows[5])
	}
	if buf.Rows[29] != "line 29" {
		t.Errorf("row 29: got %q", buf.Rows[29])
	}

	// Reset
	buf.Reset()
	for i := range buf.Rows {
		if buf.Rows[i] != "" {
			t.Errorf("row %d not empty after reset: %q", i, buf.Rows[i])
		}
	}
}

func TestDiff(t *testing.T) {
	prev := []string{"a", "b", "c"}
	curr := []string{"a", "X", "c", "d"}

	ops := Diff(prev, curr)
	if len(ops) != 2 { // row 2 changed, row 4 added
		t.Fatalf("expected 2 diff ops, got %d", len(ops))
	}
	if ops[0].Row != 2 || ops[0].Content != "X" {
		t.Errorf("op[0]: row=%d content=%q", ops[0].Row, ops[0].Content)
	}
}

func TestThemeColors(t *testing.T) {
	th := DefaultTheme()

	if th.AccentText("test") == "test" {
		t.Error("AccentText with ANSI256 should wrap in escapes")
	}
	if th.SuccessText("ok") == "ok" {
		t.Error("SuccessText with ANSI256 should wrap in escapes")
	}

	noColor := NoColorTheme()
	if noColor.AccentText("test") != "test" {
		t.Error("NoColorTheme AccentText should be plain text")
	}
}

// BenchmarkRenderPipeline benchmarks the full render pipeline.
func BenchmarkRenderPipeline(b *testing.B) {
	screen := &Screen{Width: 80, Height: 24, Theme: DefaultTheme(), out: nil}
	r := NewRenderer(screen)

	events := []core.Event{
		{Type: core.EventSystem, SessionID: "bench", Model: "test"},
		{Type: core.EventUser, Message: core.Message{Role: "user", Content: []core.ContentBlock{{Type: core.BlockText, Text: "Hello, world!"}}}},
		{Type: core.EventAssistant, Message: core.Message{Role: "assistant", Content: []core.ContentBlock{
			{Type: core.BlockText, Text: "Here is a response with some content that is reasonably long to simulate typical chat output."},
		}}},
		{Type: core.EventResult, Subtype: "success", DurationMS: 500, TotalUsage: core.Usage{InputTokens: 100, OutputTokens: 50}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Views.Chat.Reset()
		for _, ev := range events {
			r.HandleEvent(ev)
		}
	}
}
