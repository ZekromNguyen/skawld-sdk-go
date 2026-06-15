package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

// Renderer dispatches SDK events to view renderers and manages the screen
// composition. It owns the terminal buffer and orchestrates re-renders.
type Renderer struct {
	Screen *Screen
	Buffer *Buffer
	Theme  Theme
	Views  *Views

	mu sync.Mutex
}

// Views holds the individual view renderers.
type Views struct {
	Chat    *ChatView
	Status  *StatusView
	Tools   *ToolsView
	Welcome *WelcomeView
	Toast   *ToastManager
}

// NewRenderer creates a renderer with the screen and initializes all views.
func NewRenderer(screen *Screen) *Renderer {
	buf := NewBuffer(screen.Width, screen.Height)
	r := &Renderer{
		Screen: screen,
		Buffer: buf,
		Theme:  screen.Theme,
	}
	r.Views = &Views{
		Chat:    NewChatView(screen.Width, screen.Height-2, r.Theme),
		Status:  NewStatusView(screen.Width, r.Theme),
		Tools:   NewToolsView(screen.Width, r.Theme),
		Welcome: NewWelcomeView(screen.Width, screen.Height, r.Theme),
		Toast:   &ToastManager{Max: 3},
	}
	return r
}

// Resize updates dimensions and re-renders.
func (r *Renderer) Resize() {
	r.Screen.UpdateSize()
	r.Buffer.Width = r.Screen.Width
	r.Buffer.Height = r.Screen.Height
	r.Views.Chat.Resize(r.Screen.Width, r.Screen.Height-2)
	r.Views.Welcome.Resize(r.Screen.Width, r.Screen.Height)
}

// ShowWelcome renders the welcome screen.
func (r *Renderer) ShowWelcome() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Buffer.Reset()
	r.Views.Welcome.RenderSplash(r.Buffer)
	r.Buffer.FullRender(r.Screen)
}

// ─── Event Dispatch ─────────────────────────────────────────────────────

// HandleEvent dispatches a core.Event to the appropriate view and triggers a
// re-render. This is the main entry point consumed by the run loop.
func (r *Renderer) HandleEvent(ev core.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case ev.Type == core.EventSystem:
		r.Views.Chat.AppendSystem(ev)
	case ev.Type == core.EventUser:
		r.Views.Chat.AppendUser(ev)
	case ev.Type == core.EventAssistant:
		r.Views.Chat.AppendAssistant(ev)
	case ev.Type == core.EventPartialAssistant:
		r.Views.Chat.AppendDelta(ev)
	case ev.Type == core.EventToolCallStart:
		r.Views.Tools.AppendStart(ev)
	case ev.Type == core.EventToolCallEnd:
		r.Views.Tools.AppendEnd(ev)
		r.Views.Chat.AppendToolResult(ev, r.Views.Tools)
		if ev.IsError {
			r.Views.Toast.AddToast(ToastError, fmt.Sprintf("%s failed", ev.ToolName), 5)
		} else {
			r.Views.Toast.AddToast(ToastSuccess, fmt.Sprintf("%s done", ev.ToolName), 3)
		}
	case ev.Type == core.EventPermissionRequest:
		r.Views.Chat.AppendPermission(ev)
	case ev.Type == core.EventUsage:
		r.Views.Status.UpdateUsage(ev)
	case ev.Type == core.EventCompaction:
		r.Views.Chat.AppendCompaction(ev)
		r.Views.Toast.AddToast(ToastInfo, fmt.Sprintf("Compacted: %dmsgs -> %dmsgs", ev.MessagesBefore, ev.MessagesAfter), 5)
	case ev.Type == core.EventResult:
		r.Views.Chat.AppendResult(ev)
		r.Views.Status.UpdateResult(ev)
		switch ev.Subtype {
		case "success":
			r.Views.Toast.AddToast(ToastSuccess, "Done", 3)
		case "error":
			r.Views.Toast.AddToast(ToastError, "Run failed", 5)
		case "aborted":
			r.Views.Toast.AddToast(ToastWarning, "Aborted", 3)
		}
	case ev.Type == core.EventError:
		r.Views.Chat.AppendError(ev)
		if ev.Error != nil {
			r.Views.Toast.AddToast(ToastError, ev.Error.Message, 5)
		}
	case ev.Type == core.EventSkillsLoaded:
		r.Views.Chat.AppendSkills(ev)
	}

	r.Render()
}

// Render composes the current frame: chat area on top, status bar at bottom.
func (r *Renderer) Render() {
	r.Buffer.Reset()

	chatHeight := r.Buffer.Height - 1
	if chatHeight < 1 {
		chatHeight = 1
	}

	// Render chat/tool content
	r.Views.Chat.Render(r.Buffer, 0, chatHeight)

	// Tick toast TTLs and render toasts in top-right
	r.Views.Toast.Tick()
	r.Views.Toast.RenderToasts(r.Buffer, r.Theme, r.Buffer.Width)

	// Render status bar on the last line
	statusLine := r.Views.Status.Render()
	r.Buffer.SetRow(r.Buffer.Height-1, statusLine)

	r.Buffer.Render(r.Screen)
}

// RenderTools renders the tool execution overlay.
func (r *Renderer) RenderTools() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Views.Tools.RenderActive(r.Buffer)
	r.Buffer.Render(r.Screen)
}

// ClearAndReset resets all views and clears the screen.
func (r *Renderer) ClearAndReset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Buffer.Reset()
	r.Views.Chat.Reset()
	r.Views.Tools.Reset()
	r.Buffer.FullRender(r.Screen)
}

// PrintStatusBar updates just the status bar line.
func (r *Renderer) PrintStatusBar() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Buffer.SetRow(r.Buffer.Height-1, r.Views.Status.Render())
	r.Buffer.Render(r.Screen)
}

// ─── Chat View ──────────────────────────────────────────────────────────

// ChatLine represents a single rendered line in the chat area.
type ChatLine struct {
	Role    string    // "user", "raven", "tool", "system", "error"
	Content string    // rendered text with ANSI escapes
	Raw     string    // plain text version
	Time    time.Time // when this line was added
}

// ChatView buffers the conversation display. It implements scrolling and
// manages the visible window of messages.
type ChatView struct {
	Lines     []ChatLine
	Width     int
	MaxHeight int
	Theme     Theme
	ScrollPos int // 0 = bottom (latest), positive = scrolled up
	MaxScroll int
	Streaming bool
	StreamIdx int // index of the currently streaming line, -1 if none
}

// NewChatView creates a chat view.
func NewChatView(w, maxH int, t Theme) *ChatView {
	return &ChatView{
		Width:     w,
		MaxHeight: maxH,
		Theme:     t,
		StreamIdx: -1,
	}
}

// Resize updates dimensions.
func (cv *ChatView) Resize(w, maxH int) {
	cv.Width = w
	cv.MaxHeight = maxH
}

// Reset clears all lines.
func (cv *ChatView) Reset() {
	cv.Lines = nil
	cv.ScrollPos = 0
	cv.Streaming = false
	cv.StreamIdx = -1
}

func (cv *ChatView) addLine(role, content string) {
	line := ChatLine{
		Role:    role,
		Content: content,
		Raw:     stripANSI(content),
		Time:    time.Now(),
	}
	cv.Lines = append(cv.Lines, line)
}

// AppendSystem adds a system initialization event.
func (cv *ChatView) AppendSystem(ev core.Event) {
	var sb strings.Builder
	sb.WriteString(cv.Theme.DimText("── Session "))
	sb.WriteString(cv.Theme.AccentText(ev.SessionID))
	sb.WriteString("  ·  Model ")
	sb.WriteString(cv.Theme.AccentText(string(ev.Model)))
	sb.WriteString("  ·  ")
	sb.WriteString(cv.Theme.DimText(string(ev.PermissionMode)))
	cv.addLine("system", sb.String())
}

// AppendUser adds a user message.
func (cv *ChatView) AppendUser(ev core.Event) {
	txt := extractText(ev.Message)
	prompt := txt
	if len(prompt) > 200 {
		prompt = prompt[:197] + "…"
	}
	var sb strings.Builder
	sb.WriteString(cv.Theme.Bold("You │ "))
	sb.WriteString(prompt)
	cv.addLine("user", sb.String())
	cv.addLine("divider", cv.Theme.DimText(Repeat("─", cv.Width-2)))
}

// AppendAssistant adds a full assistant message.
func (cv *ChatView) AppendAssistant(ev core.Event) {
	if cv.Streaming && cv.StreamIdx >= 0 {
		cv.Streaming = false
		cv.StreamIdx = -1
	}

	text := extractText(ev.Message)
	thinking := extractThinking(ev.Message)

	if thinking != "" {
		cv.addLine("raven", cv.Theme.ThinkingText("⋯ "+thinking))
	}

	if text != "" {
		var sb strings.Builder
		sb.WriteString(cv.Theme.Bold("Raven │ "))
		sb.WriteString(text)
		cv.addLine("raven", sb.String())
	}

	// Tool uses in message — show with icons
	for _, block := range ev.Message.Content {
		if block.Type == core.BlockToolUse {
			var sb strings.Builder
			icon := ToolIcon(block.Name)
			sb.WriteString(cv.Theme.Bold("  "+icon+" [") + cv.Theme.MutedText(block.Name) + cv.Theme.Bold("]"))
			if desc := summarizeInput(block.Name, block.Input); desc != "" {
				sb.WriteString(" " + cv.Theme.DimText(desc))
			}
			cv.addLine("tool", sb.String())
		}
	}
}

// AppendDelta handles streaming text updates.
func (cv *ChatView) AppendDelta(ev core.Event) {
	text, ok := ev.Delta["text"].(string)
	if !ok {
		return
	}

	if !cv.Streaming {
		cv.Streaming = true
		cv.StreamIdx = len(cv.Lines)
		var sb strings.Builder
		sb.WriteString(cv.Theme.Bold("Raven │ "))
		sb.WriteString(text)
		sb.WriteString(cv.Theme.AccentText("█"))
		cv.Lines = append(cv.Lines, ChatLine{
			Role:    "raven",
			Content: sb.String(),
			Time:    time.Now(),
		})
	} else if cv.StreamIdx >= 0 && cv.StreamIdx < len(cv.Lines) {
		var sb strings.Builder
		sb.WriteString(cv.Theme.Bold("Raven │ "))
		sb.WriteString(text)
		sb.WriteString(cv.Theme.AccentText("█"))
		cv.Lines[cv.StreamIdx].Content = sb.String()
	}
}

// AppendPermission adds a permission prompt.
func (cv *ChatView) AppendPermission(ev core.Event) {
	for _, req := range ev.Requests {
		var sb strings.Builder
		sb.WriteString(cv.Theme.WarningText("⚑"))
		sb.WriteString(" [")
		sb.WriteString(cv.Theme.MutedText(req.ToolName))
		sb.WriteString("] ")
		sb.WriteString(req.Summary)
		sb.WriteString("  ")
		sb.WriteString(cv.Theme.Bold("[Y/a/n]"))
		cv.addLine("permission", sb.String())
	}
}

// AppendCompaction adds a compaction notification.
func (cv *ChatView) AppendCompaction(ev core.Event) {
	msg := fmt.Sprintf("⟳ Compacted: %d → %d messages (%s → %s tokens)",
		ev.MessagesBefore, ev.MessagesAfter,
		TokenFormat(ev.TokensBefore), TokenFormat(ev.TokensAfter),
	)
	cv.addLine("system", cv.Theme.DimText(msg))
}

// AppendResult adds the run result.
func (cv *ChatView) AppendResult(ev core.Event) {
	if cv.Streaming && cv.StreamIdx >= 0 {
		cv.Streaming = false
		cv.StreamIdx = -1
	}

	switch ev.Subtype {
	case "success":
		cv.addLine("result", cv.Theme.SuccessText("✓ Done · "+DurationFormat(ev.DurationMS)+" · "+TokenFormat(ev.TotalUsage.InputTokens+ev.TotalUsage.OutputTokens)+" tokens"))
	case "error":
		cv.addLine("result", cv.Theme.ErrorText("✗ Failed · "+string(ev.StopReason)))
	case "aborted":
		cv.addLine("result", cv.Theme.WarningText("⊘ Aborted"))
	}
}

// AppendError adds an error event.
func (cv *ChatView) AppendError(ev core.Event) {
	if ev.Error != nil {
		msg := cv.Theme.ErrorText("⚠ " + ev.Error.Name + ": " + ev.Error.Message)
		cv.addLine("error", msg)
	}
}

// AppendSkills adds a skills loaded notification.
func (cv *ChatView) AppendSkills(ev core.Event) {
	if names, ok := ev.Delta["skills"].([]interface{}); ok && len(names) > 0 {
		var nameStrs []string
		for _, n := range names {
			nameStrs = append(nameStrs, fmt.Sprint(n))
		}
		cv.addLine("system", cv.Theme.DimText("Skills loaded: "+strings.Join(nameStrs, ", ")))
	}
}

// AppendToolResult renders a tool result — including a diff for edit/write tools.
func (cv *ChatView) AppendToolResult(ev core.Event, tv *ToolsView) {
	tr := tv.FindByID(ev.ToolUseID)
	if tr == nil {
		return
	}

	// Status line with tool icon
	var sb strings.Builder
	icon := "✓"
	ic := ToolIcon(tr.Name)
	colorFn := cv.Theme.SuccessText
	if ev.IsError {
		icon = "✗"
		colorFn = cv.Theme.ErrorText
	}
	sb.WriteString(colorFn(fmt.Sprintf("  %s %s [%s]", icon, ic, tr.Name)))
	sb.WriteString(" " + cv.Theme.DimText(summarizeInput(tr.Name, tr.Input)))
	sb.WriteString(" · " + cv.Theme.DimText(DurationFormat(ev.DurationMS)))
	cv.addLine("tool-result", sb.String())

	// Render diff for edit/write tools
	if !ev.IsError && (tr.Name == "Edit" || tr.Name == "Write") {
		diffLines := cv.renderToolDiff(tr)
		for _, dl := range diffLines {
			cv.addLine("diff", dl)
		}
	}
}

func (cv *ChatView) renderToolDiff(tr *ToolRecord) []string {
	var lines []string

	switch tr.Name {
	case "Edit":
		oldStr, _ := tr.Input["old_string"].(string)
		newStr, _ := tr.Input["new_string"].(string)
		if oldStr != "" || newStr != "" {
			diff := UnifiedDiff(oldStr, newStr, "old", "new", 30)
			for _, dl := range diff {
				lines = append(lines, cv.Theme.DimText("  │ ")+RenderDiffLine(dl, cv.Theme))
			}
		}

	case "Write":
		content, _ := tr.Input["content"].(string)
		filePath, _ := tr.Input["file_path"].(string)
		if content != "" {
			contentLines := strings.Split(content, "\n")
			if len(contentLines) > 10 {
				lines = append(lines, cv.Theme.DimText(fmt.Sprintf("  │ %s: wrote %d lines", filePath, len(contentLines))))
				for _, l := range contentLines[:10] {
					lines = append(lines, cv.Theme.DimText("  │ ")+cv.Theme.SuccessText("+ ")+l)
				}
				lines = append(lines, cv.Theme.DimText(fmt.Sprintf("  │ ... and %d more lines", len(contentLines)-10)))
			} else {
				lines = append(lines, cv.Theme.DimText(fmt.Sprintf("  │ %s: wrote %d lines", filePath, len(contentLines))))
				for _, l := range contentLines {
					lines = append(lines, cv.Theme.DimText("  │ ")+cv.Theme.SuccessText("+ ")+l)
				}
			}
		}

	case "Bash":
		output := tr.Output
		if output != "" {
			outLines := strings.Split(output, "\n")
			limit := 15
			if len(outLines) < limit {
				limit = len(outLines)
			}
			for _, l := range outLines[:limit] {
				lines = append(lines, cv.Theme.DimText("  │ ")+l)
			}
			if len(outLines) > limit {
				lines = append(lines, cv.Theme.DimText(fmt.Sprintf("  │ ... and %d more lines", len(outLines)-limit)))
			}
		}
	}

	return lines
}

// Render writes the visible portion of the chat into the buffer.
func (cv *ChatView) Render(buf *Buffer, startRow, height int) {
	if len(cv.Lines) == 0 {
		return
	}

	visible := cv.visibleLines(height)
	row := startRow
	for _, line := range visible {
		if row >= startRow+height {
			break
		}
		buf.SetRow(row, line.Content)
		row++
	}
	cv.Fill(buf, row, startRow+height)
}

func (cv *ChatView) visibleLines(height int) []ChatLine {
	if len(cv.Lines) <= height {
		return cv.Lines
	}
	start := len(cv.Lines) - height - cv.ScrollPos
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > len(cv.Lines) {
		end = len(cv.Lines)
	}
	return cv.Lines[start:end]
}

func (cv *ChatView) Fill(buf *Buffer, from, to int) {
	for i := from; i < to; i++ {
		buf.SetRow(i, "")
	}
}

// ─── Status View ────────────────────────────────────────────────────────

// MCPStatus represents the state of MCP server connections.
type MCPStatus struct {
	Connected int
	Total     int
	Failed    int
	ToolCount int
}

// StatusView renders the bottom status bar.
type StatusView struct {
	Width         int
	Theme         Theme
	Model         string
	InputTokens   int
	OutputTokens  int
	ContextWindow int
	Cost          float64
	Mode          string // "chat", "agent", "plan"
	Step          string // "3/5" or ""
	SessionID     string
	MsgCount      int
	Idle          bool
	MCP           MCPStatus
}

// NewStatusView creates a status bar view.
func NewStatusView(w int, t Theme) *StatusView {
	return &StatusView{
		Width:         w,
		Theme:         t,
		Idle:          true,
		ContextWindow: 200000,
	}
}

// SetModel updates the model display.
func (sv *StatusView) SetModel(model string) {
	sv.Model = model
}

// SetMode updates the mode display.
func (sv *StatusView) SetMode(mode string) {
	sv.Mode = mode
}

// UpdateUsage updates from a usage event.
func (sv *StatusView) UpdateUsage(ev core.Event) {
	sv.Idle = false
	sv.InputTokens = ev.Cumulative.InputTokens
	sv.OutputTokens = ev.Cumulative.OutputTokens
	sv.Cost = estimateCost(string(ev.Model), ev.Cumulative)
}

// UpdateResult marks idle on result.
func (sv *StatusView) UpdateResult(ev core.Event) {
	sv.Idle = true
}

// SetMCPStatus updates the MCP connection state.
func (sv *StatusView) SetMCPStatus(connected, total, failed, toolCount int) {
	sv.MCP = MCPStatus{
		Connected: connected,
		Total:     total,
		Failed:    failed,
		ToolCount: toolCount,
	}
}

// Render builds the status bar line.
func (sv *StatusView) Render() string {
	var parts []string

	if sv.Idle {
		parts = append(parts, sv.Theme.AccentText(sv.Model))
		parts = append(parts, sv.Theme.DimText("idle"))
		if sv.SessionID != "" {
			parts = append(parts, sv.Theme.DimText("session: "+sv.SessionID[:min(4, len(sv.SessionID))]))
		}
		if sv.MsgCount > 0 {
			parts = append(parts, sv.Theme.DimText(fmt.Sprintf("%d msgs", sv.MsgCount)))
		}
	} else {
		parts = append(parts, sv.Theme.AccentText(sv.Model))

		// Graphical context bar
		if sv.ContextWindow > 0 {
			ctxPct := float64(sv.InputTokens) / float64(sv.ContextWindow) * 100
			bar := ProgressBarRatio(sv.InputTokens, sv.ContextWindow, 20)
			// Color thresholds
			var ctxBar string
			if ctxPct >= 80 {
				ctxBar = sv.Theme.ErrorText(bar)
			} else if ctxPct >= 50 {
				ctxBar = sv.Theme.WarningText(bar)
			} else {
				ctxBar = bar
			}
			pctStr := fmt.Sprintf("%.0f%%", ctxPct)
			ctxStr := fmt.Sprintf("ctx [%s] %s · %s/%s", ctxBar, pctStr,
				TokenFormat(sv.InputTokens), TokenFormat(sv.ContextWindow))
			parts = append(parts, sv.Theme.DimText(ctxStr))
		}

		// Cost
		parts = append(parts, sv.Theme.DimText(CostFormat(sv.Cost)))
		// Mode
		parts = append(parts, sv.Theme.MutedText(sv.Mode))
		// Step
		if sv.Step != "" {
			parts = append(parts, sv.Theme.AccentText("step "+sv.Step))
		}
	}

	// MCP status indicator
	if sv.MCP.Total > 0 {
		mcpIndicator := ""
		if sv.MCP.Failed > 0 {
			mcpIndicator = sv.Theme.WarningText(fmt.Sprintf("mcp:%d/%d⚠", sv.MCP.Connected, sv.MCP.Total))
		} else if sv.MCP.Connected > 0 {
			mcpIndicator = sv.Theme.SuccessText(fmt.Sprintf("mcp:%d/%d", sv.MCP.Connected, sv.MCP.Total))
		} else {
			mcpIndicator = sv.Theme.DimText(fmt.Sprintf("mcp:%d/%d", sv.MCP.Connected, sv.MCP.Total))
		}
		if sv.MCP.ToolCount > 0 {
			mcpIndicator += sv.Theme.DimText(fmt.Sprintf("+%dt", sv.MCP.ToolCount))
		}
		parts = append(parts, mcpIndicator)
	}

	// Right-aligned help hint
	helpHint := sv.Theme.DimText("/help")

	// Compose with spacing
	left := strings.Join(parts, " │ ")

	leftWidth := len(stripANSI(left))
	rightWidth := len(stripANSI(helpHint))
	spacer := sv.Width - leftWidth - rightWidth - 4
	if spacer < 1 {
		spacer = 1
	}

	return BoxTL + " " + left + " " + strings.Repeat(" ", spacer) + helpHint + " " + BoxTR
}

// ─── Tools View ─────────────────────────────────────────────────────────

// ToolRecord tracks an in-flight or completed tool execution.
type ToolRecord struct {
	ID        string
	Name      string
	Input     map[string]interface{}
	Started   time.Time
	Duration  int64
	IsError   bool
	Output    string
	Completed bool
}

// ToolsView tracks and renders tool executions.
type ToolsView struct {
	Width   int
	Theme   Theme
	Active  []*ToolRecord
	History []*ToolRecord
}

// NewToolsView creates a tools view.
func NewToolsView(w int, t Theme) *ToolsView {
	return &ToolsView{
		Width: w,
		Theme: t,
	}
}

// Reset clears all tool records.
func (tv *ToolsView) Reset() {
	tv.Active = nil
	tv.History = nil
}

// AppendStart records a tool start event.
func (tv *ToolsView) AppendStart(ev core.Event) {
	tr := &ToolRecord{
		ID:      ev.ToolUseID,
		Name:    ev.ToolName,
		Input:   ev.Input,
		Started: time.Now(),
	}
	tv.Active = append(tv.Active, tr)
}

// AppendEnd records a tool end event.
func (tv *ToolsView) AppendEnd(ev core.Event) {
	for _, tr := range tv.Active {
		if tr.ID == ev.ToolUseID {
			tr.Duration = ev.DurationMS
			tr.IsError = ev.IsError
			tr.Completed = true
			if output, ok := ev.Delta["output"].(string); ok {
				tr.Output = output
			} else if content, ok := ev.Delta["content"].(string); ok {
				tr.Output = content
			}
		}
	}
	var stillActive []*ToolRecord
	for _, tr := range tv.Active {
		if tr.Completed {
			tv.History = append(tv.History, tr)
		} else {
			stillActive = append(stillActive, tr)
		}
	}
	tv.Active = stillActive
}

// FindByID looks up a tool record by ID across active and history.
func (tv *ToolsView) FindByID(id string) *ToolRecord {
	for _, tr := range tv.Active {
		if tr.ID == id {
			return tr
		}
	}
	for _, tr := range tv.History {
		if tr.ID == id {
			return tr
		}
	}
	return nil
}

// RenderActive renders only active tool executions.
func (tv *ToolsView) RenderActive(buf *Buffer) {
	for i, tr := range tv.Active {
		line := tv.renderToolLine(tr)
		buf.SetRow(i, line)
	}
}

func (tv *ToolsView) renderToolLine(tr *ToolRecord) string {
	var sb strings.Builder
	icon := ToolIcon(tr.Name)
	sb.WriteString(tv.Theme.Bold(fmt.Sprintf("  %s [%s]", icon, tr.Name)))
	sb.WriteString(" ")
	sb.WriteString(tv.Theme.DimText(summarizeInput(tr.Name, tr.Input)))
	if tr.Duration > 0 {
		sb.WriteString(" · ")
		sb.WriteString(tv.Theme.DimText(DurationFormat(tr.Duration)))
	}
	if tr.Completed && tr.IsError {
		sb.WriteString(" ")
		sb.WriteString(tv.Theme.ErrorText("✗"))
	}
	return sb.String()
}

// ─── Helpers ────────────────────────────────────────────────────────────

func extractText(msg core.Message) string {
	for _, block := range msg.Content {
		if block.Type == core.BlockText && block.Text != "" {
			return block.Text
		}
	}
	return ""
}

func extractThinking(msg core.Message) string {
	for _, block := range msg.Content {
		if block.Type == core.BlockThinking && block.Thinking != "" {
			return block.Thinking
		}
	}
	return ""
}

func summarizeInput(name string, input map[string]interface{}) string {
	switch name {
	case "Read", "Glob", "Grep":
		if fp, ok := input["file_path"].(string); ok {
			return Truncate(fp, 60)
		}
		if pat, ok := input["pattern"].(string); ok {
			return pat
		}
	case "Write", "Edit":
		if fp, ok := input["file_path"].(string); ok {
			return Truncate(fp, 60)
		}
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return Truncate(cmd, 80)
		}
	case "TaskCreate":
		if sub, ok := input["subject"].(string); ok {
			return sub
		}
	case "Skill":
		if skill, ok := input["skill"].(string); ok {
			return skill
		}
	case "Subagent":
		if agent, ok := input["agent"].(string); ok {
			return agent + ": " + fmt.Sprint(input["task"])
		}
	default:
		return ""
	}
	return ""
}

func estimateCost(model string, usage core.Usage) float64 {
	inputCost := float64(usage.InputTokens) * 0.000003
	outputCost := float64(usage.OutputTokens) * 0.000015
	return inputCost + outputCost
}

func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
