package tui

import (
	"fmt"
	"strings"
)

// ─── Tool Iconography ─────────────────────────────────────────────────────

// ToolIcon returns the display icon for a given tool name.
func ToolIcon(name string) string {
	switch name {
	case "Read", "Glob", "Grep":
		return "🔍"
	case "Write", "Edit":
		return "✏️"
	case "Bash":
		return "▶️"
	case "TaskCreate", "TaskList", "TaskGet", "TaskUpdate":
		return "📋"
	case "MemoryRead", "MemoryWrite", "MemorySearch", "SessionSearch":
		return "🧠"
	case "Skill":
		return "⚡"
	case "Subagent":
		return "🤖"
	case "WebSearch", "WebFetch":
		return "🌐"
	case "BrowserNavigate", "BrowserSnapshot", "BrowserVision":
		return "🌍"
	case "Process":
		return "⏳"
	case "CronCreate", "CronList", "CronDelete":
		return "⏰"
	case "XSearch":
		return "🐦"
	case "VisionAnalyze":
		return "👁️"
	case "ImageGenerate":
		return "🎨"
	case "TextToSpeech":
		return "🔊"
	default:
		return "🔧"
	}
}

// ToolIconASCII fallback for terminals without emoji support.
func ToolIconASCII(name string) string {
	switch name {
	case "Read", "Glob", "Grep":
		return "scout"
	case "Write", "Edit":
		return "talon"
	case "Bash":
		return "exec"
	case "TaskCreate", "TaskList", "TaskGet", "TaskUpdate":
		return "task"
	case "MemoryRead", "MemoryWrite", "MemorySearch", "SessionSearch":
		return "memory"
	case "Skill":
		return "skill"
	case "Subagent":
		return "agent"
	case "WebSearch", "WebFetch":
		return "web"
	case "BrowserNavigate", "BrowserSnapshot", "BrowserVision":
		return "browser"
	case "Process":
		return "process"
	case "CronCreate", "CronList", "CronDelete":
		return "cron"
	case "XSearch":
		return "xsearch"
	case "VisionAnalyze":
		return "vision"
	case "ImageGenerate":
		return "image"
	case "TextToSpeech":
		return "tts"
	default:
		return name
	}
}

// ─── Model Picker ─────────────────────────────────────────────────────────

// ModelInfo describes an available model.
type ModelInfo struct {
	ID          string
	Name        string
	Provider    string
	Description string
	ContextSize int
}

// AvailableModels lists all known models grouped by provider.
var AvailableModels = []ModelInfo{
	// Anthropic
	{ID: "claude-opus-4-8", Name: "claude-opus-4-8", Provider: "Anthropic", Description: "Fast, best for complex reasoning", ContextSize: 200000},
	{ID: "claude-sonnet-4-6", Name: "claude-sonnet-4-6", Provider: "Anthropic", Description: "Balanced speed + capability", ContextSize: 200000},
	{ID: "claude-haiku-4-5", Name: "claude-haiku-4-5", Provider: "Anthropic", Description: "Fastest, best for simple tasks", ContextSize: 200000},
	// OpenAI
	{ID: "gpt-5.2-code", Name: "gpt-5.2-code", Provider: "OpenAI", Description: "Strong coding, large context", ContextSize: 200000},
	{ID: "gpt-5.2-mini", Name: "gpt-5.2-mini", Provider: "OpenAI", Description: "Fast, affordable coding", ContextSize: 128000},
}

// ModelPicker renders the interactive model selection modal.
type ModelPicker struct {
	Width  int
	Height int
	Theme  Theme
}

// RenderModelPicker renders the model picker overlay.
func (mp *ModelPicker) RenderModelPicker(buf *Buffer, currentModel string, entries []ModelInfo, selected int) {
	// Filter to visible entries
	width := mp.Width - 8
	if width > 64 {
		width = 64
	}
	if width < 40 {
		width = 40
	}
	leftPad := (mp.Width - width) / 2

	maxVisible := mp.Height - 8
	if maxVisible < 4 {
		maxVisible = 4
	}
	if maxVisible > len(entries) {
		maxVisible = len(entries)
	}
	contentH := maxVisible + 5
	topPad := (mp.Height - contentH) / 2
	if topPad < 1 {
		topPad = 1
	}

	// Clear
	for i := 0; i < mp.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad

	// Header
	title := " Switch Model "
	buf.SetRow(row, padTo(leftPad)+mp.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	// Current
	currentLine := mp.Theme.DimText("Current: ") + mp.Theme.AccentText(currentModel)
	buf.SetRow(row, padTo(leftPad)+mp.Theme.DimText(BoxV+" ")+currentLine+padTo(width-len(stripANSI(currentLine))-2)+mp.Theme.DimText(BoxV))
	row++

	// Separator
	buf.SetRow(row, padTo(leftPad)+mp.Theme.DimText(BoxV+Repeat(BoxH, width-1)+BoxV))
	row++

	// Scroll window
	startIdx := 0
	if selected >= maxVisible {
		startIdx = selected - maxVisible + 1
	}

	currentProvider := ""
	for i, entry := range entries[startIdx : startIdx+maxVisible] {
		idx := i + startIdx

		// Provider header
		if entry.Provider != currentProvider {
			currentProvider = entry.Provider
			provHdr := mp.Theme.Bold(currentProvider)
			buf.SetRow(row, padTo(leftPad)+mp.Theme.DimText(BoxV+" ")+provHdr+padTo(width-len(stripANSI(provHdr))-2)+mp.Theme.DimText(BoxV))
			row++
		}

		// Selection marker
		marker := "  "
		if entry.ID == currentModel {
			marker = mp.Theme.SuccessText("● ")
		}
		arrow := "  "
		if idx == selected {
			arrow = mp.Theme.AccentText("▸ ")
		}

		line := mp.Theme.DimText(BoxV+" ") + arrow + marker + mp.Theme.Bold(entry.Name)
		desc := mp.Theme.DimText("  " + entry.Description)
		full := line + desc
		padding := padTo(width - len(stripANSI(full)) - 1)
		buf.SetRow(row, padTo(leftPad)+full+padding+mp.Theme.DimText(BoxV))
		row++
	}

	// Fill remaining
	for row < topPad+contentH-1 {
		buf.SetRow(row, padTo(leftPad)+mp.Theme.DimText(BoxV)+padTo(width-1)+mp.Theme.DimText(BoxV))
		row++
	}

	// Footer
	buf.SetRow(row, padTo(leftPad)+mp.Theme.DimText(BoxV+" ")+mp.Theme.DimText("↑↓ select  Enter confirm  Esc cancel")+padTo(width-len("↑↓ select  Enter confirm  Esc cancel")-2)+mp.Theme.DimText(BoxV))
	row++

	// Bottom border
	buf.SetRow(row, padTo(leftPad)+mp.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Settings Page ────────────────────────────────────────────────────────

// SettingsPage renders an interactive settings editor.
type SettingsPage struct {
	Width  int
	Height int
	Theme  Theme
}

// SettingsSection represents a group of settings.
type SettingsSection struct {
	Name   string
	Fields []SettingsField
}

// SettingsField is one editable setting.
type SettingsField struct {
	Label string
	Value string
	Hint  string
}

// buildSettings constructs the settings list from current state.
func BuildSettings(model string, permissionMode string) []SettingsSection {
	return []SettingsSection{
		{
			Name: "Model",
			Fields: []SettingsField{
				{Label: "Provider", Value: "anthropic", Hint: ""},
				{Label: "Model", Value: model, Hint: ""},
			},
		},
		{
			Name: "Behavior",
			Fields: []SettingsField{
				{Label: "Permissions", Value: permissionMode, Hint: "ask before writes"},
				{Label: "Max turns", Value: "1000", Hint: ""},
				{Label: "Compaction", Value: "auto", Hint: "at 80% context"},
			},
		},
		{
			Name: "Appearance",
			Fields: []SettingsField{
				{Label: "Theme", Value: "default", Hint: "follow terminal"},
				{Label: "Timestamps", Value: "off", Hint: ""},
				{Label: "Tool icons", Value: "on", Hint: ""},
			},
		},
	}
}

// RenderSettings renders the settings page overlay.
func (sp *SettingsPage) RenderSettings(buf *Buffer, sections []SettingsSection, selectedSection, selectedField int) {
	width := sp.Width - 8
	if width > 60 {
		width = 60
	}
	if width < 40 {
		width = 40
	}
	leftPad := (sp.Width - width) / 2

	// Count total fields
	totalFields := 0
	fieldIdx := 0
	_ = 0 // currentSection placeholder
	currentField := 0
	for i, s := range sections {
		for j := range s.Fields {
			if totalFields == selectedField {
				_ = i // currentSection tracking
				_ = j // currentField tracking
			}
			totalFields++
		}
	}
	_ = fieldIdx
	_ = currentField

	contentH := totalFields + len(sections)*2 + 4
	topPad := (sp.Height - contentH) / 2
	if topPad < 1 {
		topPad = 1
	}

	// Clear
	for i := 0; i < sp.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad

	// Header
	title := " Settings "
	buf.SetRow(row, padTo(leftPad)+sp.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	globalIdx := 0
	for _, sec := range sections {
		// Section header
		secHdr := sp.Theme.Bold(sec.Name)
		buf.SetRow(row, padTo(leftPad)+sp.Theme.DimText(BoxV+" ")+secHdr+padTo(width-len(stripANSI(secHdr))-2)+sp.Theme.DimText(BoxV))
		row++

		for _, field := range sec.Fields {
			marker := "  "
			if globalIdx == selectedField {
				marker = sp.Theme.AccentText("▸ ")
			}

			label := marker + field.Label
			value := sp.Theme.AccentText(field.Value)
			if field.Hint != "" {
				value += "  " + sp.Theme.DimText("("+field.Hint+")")
			}

			line := sp.Theme.DimText(BoxV+" ") + label
			full := line + padTo(width-len(stripANSI(line))-len(stripANSI(value))-1) + value
			buf.SetRow(row, padTo(leftPad)+full+sp.Theme.DimText(BoxV))
			row++
			globalIdx++
		}
	}

	// Fill
	for row < topPad+contentH-1 {
		buf.SetRow(row, padTo(leftPad)+sp.Theme.DimText(BoxV)+padTo(width-1)+sp.Theme.DimText(BoxV))
		row++
	}

	// Footer
	buf.SetRow(row, padTo(leftPad)+sp.Theme.DimText(BoxV+" ")+sp.Theme.DimText("↑↓ navigate  Enter edit  Esc back")+padTo(width-len("↑↓ navigate  Enter edit  Esc back")-2)+sp.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+sp.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

func (sp *SettingsPage) selectedField(sections []SettingsSection, globalIdx int) (sectionIdx, fieldIdx int) {
	count := 0
	for i, sec := range sections {
		for j := range sec.Fields {
			if count == globalIdx {
			return i, j
			}
			count++
		}
	}
	return -1, -1
}

// ─── Session Browser ──────────────────────────────────────────────────────

// SessionInfo describes a saved session.
type SessionInfo struct {
	ID       string
	Name     string
	MsgCount int
	Topic    string
	Active   bool
	TimeAgo  string
}

// SessionBrowser renders the session list.
type SessionBrowser struct {
	Width  int
	Height int
	Theme  Theme
}

// RenderSessions renders the session browser overlay.
func (sb *SessionBrowser) RenderSessions(buf *Buffer, sessions []SessionInfo, selected int) {
	width := sb.Width - 8
	if width > 70 {
		width = 70
	}
	if width < 40 {
		width = 40
	}
	leftPad := (sb.Width - width) / 2

	maxVisible := sb.Height - 6
	if maxVisible > len(sessions) {
		maxVisible = len(sessions)
	}
	if maxVisible < 1 {
		maxVisible = 1
	}
	contentH := maxVisible + 4
	topPad := (sb.Height - contentH) / 2
	if topPad < 1 {
		topPad = 1
	}

	for i := 0; i < sb.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad
	title := " Sessions "
	buf.SetRow(row, padTo(leftPad)+sb.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	if len(sessions) == 0 {
		empty := sb.Theme.DimText("  No saved sessions yet.")
		buf.SetRow(row, padTo(leftPad)+sb.Theme.DimText(BoxV+" ")+empty+padTo(width-len(stripANSI(empty))-2)+sb.Theme.DimText(BoxV))
		row++
	} else {
		startIdx := 0
		if selected >= maxVisible {
			startIdx = selected - maxVisible + 1
		}

		for i, s := range sessions[startIdx : startIdx+maxVisible] {
			idx := i + startIdx

			activeMark := "  "
			if s.Active {
				activeMark = sb.Theme.SuccessText("● ")
			}
			marker := "  "
			if idx == selected {
				marker = sb.Theme.AccentText("▸ ")
			}

			id := sb.Theme.AccentText(s.ID[:minInt(4, len(s.ID))])
			line := sb.Theme.DimText(BoxV+" ") + marker + activeMark + id +
				"  " + sb.Theme.Bold(s.Name) +
				"  " + sb.Theme.DimText(fmt.Sprintf("%d msgs", s.MsgCount))
			if s.Topic != "" {
				line += "  " + sb.Theme.DimText(Truncate(s.Topic, 20))
			}
			line += "  " + sb.Theme.DimText(s.TimeAgo)

			padding := padTo(width - len(stripANSI(line)) - 1)
			buf.SetRow(row, padTo(leftPad)+line+padding+sb.Theme.DimText(BoxV))
			row++
		}
	}

	for row < topPad+contentH-1 {
		buf.SetRow(row, padTo(leftPad)+sb.Theme.DimText(BoxV)+padTo(width-1)+sb.Theme.DimText(BoxV))
		row++
	}

	buf.SetRow(row, padTo(leftPad)+sb.Theme.DimText(BoxV+" ")+sb.Theme.DimText("Enter resume  n new  d delete  Esc back")+padTo(width-len("Enter resume  n new  d delete  Esc back")-2)+sb.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+sb.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Memory Browser ───────────────────────────────────────────────────────

// MemoryEntry represents one memory file.
type MemoryEntry struct {
	Name        string
	Content     string
	Category    string // project, feedback, reference
	Description string
}

// MemoryBrowser renders the memory list.
type MemoryBrowser struct {
	Width  int
	Height int
	Theme  Theme
}

// RenderMemories renders the memory browser overlay.
func (mb *MemoryBrowser) RenderMemories(buf *Buffer, memories []MemoryEntry, selected int) {
	width := mb.Width - 8
	if width > 65 {
		width = 65
	}
	if width < 40 {
		width = 40
	}
	leftPad := (mb.Width - width) / 2

	// Group by category
	categories := make(map[string][]MemoryEntry)
	for _, m := range memories {
		categories[m.Category] = append(categories[m.Category], m)
	}
	catOrder := []string{"project", "feedback", "reference"}

	totalItems := 0
	for _, cat := range catOrder {
		if entries, ok := categories[cat]; ok {
			totalItems += len(entries) + 1 // +1 for category header
		}
	}
	if totalItems == 0 {
		totalItems = 1
	}

	contentH := totalItems + 3
	if contentH > mb.Height-2 {
		contentH = mb.Height - 2
	}
	topPad := (mb.Height - contentH) / 2
	if topPad < 1 {
		topPad = 1
	}

	for i := 0; i < mb.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad
	title := " Memories "
	buf.SetRow(row, padTo(leftPad)+mb.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	globalIdx := 0
	if len(memories) == 0 {
		empty := mb.Theme.DimText("  No saved memories.")
		buf.SetRow(row, padTo(leftPad)+mb.Theme.DimText(BoxV+" ")+empty+padTo(width-len(stripANSI(empty))-2)+mb.Theme.DimText(BoxV))
		row++
	} else {
		for _, cat := range catOrder {
			entries, ok := categories[cat]
			if !ok || len(entries) == 0 {
				continue
			}
			// Category header
			catHdr := mb.Theme.Bold(fmt.Sprintf("  %s (%d)", cat, len(entries)))
			buf.SetRow(row, padTo(leftPad)+mb.Theme.DimText(BoxV)+catHdr+padTo(width-len(stripANSI(catHdr))-1)+mb.Theme.DimText(BoxV))
			row++
			globalIdx++

			for _, entry := range entries {
				marker := "  "
				if globalIdx == selected {
					marker = mb.Theme.AccentText("▸ ")
				}
				content := Truncate(entry.Content, width-10)
				line := mb.Theme.DimText(BoxV+" ") + marker + mb.Theme.Bold(content)
				padding := padTo(width - len(stripANSI(line)) - 1)
				buf.SetRow(row, padTo(leftPad)+line+padding+mb.Theme.DimText(BoxV))
				row++
				globalIdx++
			}
		}
	}

	for row < topPad+contentH-1 {
		buf.SetRow(row, padTo(leftPad)+mb.Theme.DimText(BoxV)+padTo(width-1)+mb.Theme.DimText(BoxV))
		row++
	}

	buf.SetRow(row, padTo(leftPad)+mb.Theme.DimText(BoxV+" ")+mb.Theme.DimText("Enter view  e edit  d delete  Esc back")+padTo(width-len("Enter view  e edit  d delete  Esc back")-2)+mb.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+mb.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Setup Wizard ─────────────────────────────────────────────────────────

// SetupWizard renders the first-launch setup screen.
type SetupWizard struct {
	Width  int
	Height int
	Theme  Theme
}

// SetupStep represents one configurable item.
type SetupStep struct {
	Section string
	Items   []SetupItem
}

// SetupItem is one choice in a setup step.
type SetupItem struct {
	Label    string
	Detail   string
	Selected bool
	Valid    bool
}

// buildSetupSteps detects environment state.
func buildSetupSteps(providerID, model, cwd string, hasAnthropicKey, hasOpenAIKey bool) []SetupStep {
	return []SetupStep{
		{
			Section: "Provider & Key",
			Items: []SetupItem{
				{
					Label:    "Anthropic",
					Detail:   maskedKey("ANTHROPIC_API_KEY", hasAnthropicKey),
					Selected: providerID == "anthropic",
					Valid:    hasAnthropicKey,
				},
				{
					Label:    "OpenAI",
					Detail:   maskedKey("OPENAI_API_KEY", hasOpenAIKey),
					Selected: providerID == "openai-responses",
					Valid:    hasOpenAIKey,
				},
			},
		},
		{
			Section: "Model",
			Items: []SetupItem{
				{Label: model, Detail: "Balanced speed and capability · 200K context", Selected: true, Valid: true},
			},
		},
		{
			Section: "Permissions",
			Items: []SetupItem{
				{Label: "default", Detail: "Ask before editing files or running commands", Selected: true, Valid: true},
				{Label: "acceptEdits", Detail: "Auto-approve edits, ask before commands", Selected: false, Valid: true},
				{Label: "yolo", Detail: "Run everything without asking", Selected: false, Valid: true},
			},
		},
		{
			Section: "Nest (working directory)",
			Items: []SetupItem{
				{Label: cwd, Detail: "", Selected: true, Valid: true},
			},
		},
	}
}

func maskedKey(name string, set bool) string {
	if set {
		return name + "  ····**** ✓"
	}
	return name + "  not set  —"
}

// RenderSetup renders the first-launch setup wizard.
func (sw *SetupWizard) RenderSetup(buf *Buffer, steps []SetupStep, selectedSection, selectedItem int) {
	width := sw.Width - 8
	if width > 70 {
		width = 70
	}
	if width < 50 {
		width = 50
	}
	leftPad := (sw.Width - width) / 2

	// Count total lines
	totalLines := 2 // header
	for _, step := range steps {
		totalLines++ // section separator
		totalLines++ // section header
		for range step.Items {
			totalLines++ // each item
		}
		totalLines++ // card bottom
	}
	totalLines += 3 // footer

	topPad := (sw.Height - totalLines) / 2
	if topPad < 1 {
		topPad = 1
	}

	for i := 0; i < sw.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad

	// Header
	title := " Welcome. Let's get you set up. "
	buf.SetRow(row, padTo(leftPad)+sw.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	// Raven mark
	buf.SetRow(row, padTo(leftPad)+sw.Theme.DimText(BoxV+" ")+sw.Theme.AccentText("◤")+padTo(width-3)+sw.Theme.DimText(BoxV))
	row++

	globalIdx := 0
	for si, step := range steps {
		// Divider
		buf.SetRow(row, padTo(leftPad)+sw.Theme.DimText(BoxV+Repeat("─", width-1)+BoxV))
		row++

		// Section header
		secHdr := sw.Theme.Bold(step.Section)
		buf.SetRow(row, padTo(leftPad)+sw.Theme.DimText(BoxV+" ")+secHdr+padTo(width-len(stripANSI(secHdr))-2)+sw.Theme.DimText(BoxV))
		row++

		// Card-like items
		for ii, item := range step.Items {
			marker := "  "
			if si == selectedSection && ii == selectedItem {
				marker = sw.Theme.AccentText("▸ ")
			}

			radio := "○ "
			if item.Selected {
				radio = sw.Theme.AccentText("● ")
			}
			check := ""
			if item.Valid && strings.Contains(item.Detail, "····") {
				check = sw.Theme.SuccessText("  ✓")
			} else if !item.Valid && strings.Contains(item.Detail, "not set") {
				check = sw.Theme.ErrorText("  —")
			}

			label := marker + radio + sw.Theme.Bold(item.Label) + check
			line := sw.Theme.DimText(BoxV+" ") + label
			if item.Detail != "" {
				detail := sw.Theme.DimText("  " + item.Detail)
				line += detail
			}
			padding := padTo(width - len(stripANSI(line)) - 1)
			buf.SetRow(row, padTo(leftPad)+line+padding+sw.Theme.DimText(BoxV))
			row++
			globalIdx++
		}
	}

	// Bottom area
	buf.SetRow(row, padTo(leftPad)+sw.Theme.DimText(BoxV+Repeat("─", width-1)+BoxV))
	row++

	footer := sw.Theme.Bold("Enter") + sw.Theme.DimText(" begin    ") + sw.Theme.DimText("↑↓") + sw.Theme.DimText(" navigate    ") + sw.Theme.DimText("/help") + sw.Theme.DimText("    Esc quit")
	buf.SetRow(row, padTo(leftPad)+sw.Theme.DimText(BoxV+" ")+footer+padTo(width-len(stripANSI(footer))-2)+sw.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+sw.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Export Dialog ────────────────────────────────────────────────────────

// ExportDialog shows export format options and progress.
type ExportDialog struct {
	Width  int
	Height int
	Theme  Theme
}

// RenderExport renders the export dialog.
func (ed *ExportDialog) RenderExport(buf *Buffer, format string, exporting bool, progress int, outputPath string) {
	width := ed.Width - 8
	if width > 50 {
		width = 50
	}
	if width < 36 {
		width = 36
	}
	leftPad := (ed.Width - width) / 2
	topPad := ed.Height/2 - 4

	for i := 0; i < ed.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad

	if exporting {
		title := " Exporting... "
		buf.SetRow(row, padTo(leftPad)+ed.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
		row++

		progressBar := ProgressBarRatio(progress, 100, width-4)
		pctLine := ed.Theme.DimText(BoxV+" ") + progressBar + ed.Theme.DimText(fmt.Sprintf(" %d%%", progress))
		buf.SetRow(row, padTo(leftPad)+pctLine+padTo(width-len(stripANSI(pctLine))-1)+ed.Theme.DimText(BoxV))
		row++

		if outputPath != "" {
			pathLine := ed.Theme.DimText(BoxV+" ") + "Saved: " + ed.Theme.AccentText(outputPath)
			buf.SetRow(row, padTo(leftPad)+pathLine+padTo(width-len(stripANSI(pathLine))-1)+ed.Theme.DimText(BoxV))
			row++
		}
	} else {
		title := " Export Conversation "
		buf.SetRow(row, padTo(leftPad)+ed.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
		row++

		formats := []string{"md", "json", "txt"}
		for _, f := range formats {
			marker := "  "
			if f == format {
				marker = ed.Theme.AccentText("● ")
			}
			line := ed.Theme.DimText(BoxV+" ") + marker + ed.Theme.Bold("."+f)
			padding := padTo(width - len(stripANSI(line)) - 1)
			buf.SetRow(row, padTo(leftPad)+line+padding+ed.Theme.DimText(BoxV))
			row++
		}
	}

	for row < topPad+5 {
		buf.SetRow(row, padTo(leftPad)+ed.Theme.DimText(BoxV)+padTo(width-1)+ed.Theme.DimText(BoxV))
		row++
	}

	buf.SetRow(row, padTo(leftPad)+ed.Theme.DimText(BoxV+" ")+ed.Theme.DimText("Enter export  Esc cancel")+padTo(width-len("Enter export  Esc cancel")-2)+ed.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+ed.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Cost Breakdown ───────────────────────────────────────────────────────

// CostDialog shows a detailed cost breakdown.
type CostDialog struct {
	Width  int
	Height int
	Theme  Theme
}

// CostData holds the cost details to display.
type CostData struct {
	Model        string
	InputTokens  int
	OutputTokens int
	InputCost    float64
	OutputCost   float64
	TotalCost    float64
	DurationMS   int64
}

// RenderCost renders the cost breakdown dialog.
func (cd *CostDialog) RenderCost(buf *Buffer, data CostData) {
	width := cd.Width - 8
	if width > 50 {
		width = 50
	}
	if width < 36 {
		width = 36
	}
	leftPad := (cd.Width - width) / 2
	topPad := cd.Height/2 - 5

	for i := 0; i < cd.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad

	title := " Cost Breakdown "
	buf.SetRow(row, padTo(leftPad)+cd.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	modelLine := cd.Theme.DimText("Model: ") + cd.Theme.AccentText(data.Model)
	buf.SetRow(row, padTo(leftPad)+cd.Theme.DimText(BoxV+" ")+modelLine+padTo(width-len(stripANSI(modelLine))-2)+cd.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+cd.Theme.DimText(BoxV+Repeat(BoxH, width-1)+BoxV))
	row++

	lines := []string{
		fmt.Sprintf("Input:    %s tokens · %s", TokenFormat(data.InputTokens), CostFormat(data.InputCost)),
		fmt.Sprintf("Output:   %s tokens · %s", TokenFormat(data.OutputTokens), CostFormat(data.OutputCost)),
		fmt.Sprintf("Total:    %s", cd.Theme.Bold(CostFormat(data.TotalCost))),
		fmt.Sprintf("Duration: %s", DurationFormat(data.DurationMS)),
	}
	for _, l := range lines {
		buf.SetRow(row, padTo(leftPad)+cd.Theme.DimText(BoxV+" ")+cd.Theme.DimText(l)+padTo(width-len(stripANSI(l))-2)+cd.Theme.DimText(BoxV))
		row++
	}

	for row < topPad+8 {
		buf.SetRow(row, padTo(leftPad)+cd.Theme.DimText(BoxV)+padTo(width-1)+cd.Theme.DimText(BoxV))
		row++
	}

	buf.SetRow(row, padTo(leftPad)+cd.Theme.DimText(BoxV+" ")+cd.Theme.DimText("Esc to close")+padTo(width-len("Esc to close")-2)+cd.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+cd.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Agent Mode View ──────────────────────────────────────────────────────

// AgentView renders agent mode execution with step visualization.
type AgentView struct {
	Width  int
	Height int
	Theme  Theme
}

// AgentStep represents one step in an agent plan.
type AgentStep struct {
	Number   int
	Desc     string
	State    rune // 'o' pending, 'a' active, 'd' done, 'e' error, 's' skipped
	Duration int64
	Result   string
}

// RenderAgent renders the agent mode view.
func (av *AgentView) RenderAgent(buf *Buffer, goal string, steps []AgentStep, activeContent string, expanded bool) {
	for i := 0; i < av.Height; i++ {
		buf.SetRow(i, "")
	}

	row := 0

	// Goal
	goalLine := av.Theme.Bold("Goal │ ") + av.Theme.AccentText(goal)
	buf.SetRow(row, goalLine)
	row += 2

	if expanded {
		// Divider
		buf.SetRow(row, av.Theme.DimText(Repeat("═", av.Width)))
		row++

		// Steps
		for _, step := range steps {
			var stateIcon string
			var colorFn func(string) string
			switch step.State {
		case 'd':
				stateIcon = "✓"
				colorFn = av.Theme.SuccessText
		case 'a':
				stateIcon = "◉"
				colorFn = av.Theme.AccentText
		case 'e':
				stateIcon = "✗"
				colorFn = av.Theme.ErrorText
		case 's':
				stateIcon = "⊘"
				colorFn = av.Theme.MutedText
			default:
				stateIcon = "○"
				colorFn = av.Theme.DimText
			}

			duration := ""
			if step.Duration > 0 {
				duration = av.Theme.DimText("  " + DurationFormat(step.Duration))
			}
			result := ""
			if step.Result != "" {
				result = "  " + av.Theme.DimText(Truncate(step.Result, 40))
			}

			var progressBar string
			if step.State == 'a' {
				progressBar = ProgressBarRatio(int(step.Duration), 3000, 12)
			} else if step.State == 'd' {
				progressBar = av.Theme.SuccessText(Repeat("█", 12))
			} else {
				progressBar = av.Theme.DimText(Repeat(" ", 12))
			}

			line := fmt.Sprintf("  %s %d. %s  %s%s%s",
				colorFn(stateIcon), step.Number,
				Truncate(step.Desc, 30),
				progressBar, duration, result)
			buf.SetRow(row, line)
			row++
		}

		// Active content area
		if activeContent != "" {
			buf.SetRow(row, av.Theme.DimText(Repeat("═", av.Width)))
			row++
			for _, l := range strings.Split(activeContent, "\n")[:5] {
				buf.SetRow(row, av.Theme.DimText("  "+l))
				row++
			}
		}
	} else {
		// Compact view
		var indicators string
		done := 0
		total := len(steps)
		active := 0
		for _, s := range steps {
			switch s.State {
		case 'd':
				indicators += av.Theme.SuccessText("✓")
				done++
		case 'a':
				indicators += av.Theme.AccentText("◉")
				active = s.Number
		case 'e':
				indicators += av.Theme.ErrorText("✗")
		case 's':
				indicators += av.Theme.MutedText("⊘")
			default:
				indicators += av.Theme.DimText("○")
			}
		}

		var progressLine string
		if active > 0 {
			progressLine = fmt.Sprintf("%s  Step %d/%d · %d of %d steps complete · %s elapsed",
				indicators, active, total, done, total, "calculating...")
		} else {
			progressLine = fmt.Sprintf("%s  %d/%d done", indicators, done, total)
		}
		buf.SetRow(row, progressLine)
		row++

		buf.SetRow(row, av.Theme.DimText("[Expand for details — press Ctrl+E]"))
	}

	// Fill remaining
	for row < av.Height {
		buf.SetRow(row, "")
		row++
	}
}

// ─── Theme Switcher ───────────────────────────────────────────────────────

// ThemeSwitcher renders the theme selection modal.
type ThemeSwitcher struct {
	Width  int
	Height int
	Theme  Theme
}

// AvailableTheme describes a selectable theme.
type AvailableTheme struct {
	ID          string
	Name        string
	Description string
}

// Themes available for selection.
var AvailableThemes = []AvailableTheme{
	{ID: "default", Name: "Default", Description: "Follow terminal theme"},
	{ID: "dark", Name: "Dark", Description: "Optimized for dark terminals"},
	{ID: "light", Name: "Light", Description: "Higher contrast for light backgrounds"},
	{ID: "no-color", Name: "No Color", Description: "Strip all ANSI escapes (NO_COLOR)"},
}

// RenderTheme renders the theme switcher.
func (ts *ThemeSwitcher) RenderTheme(buf *Buffer, currentTheme string, themes []AvailableTheme, selected int) {
	width := ts.Width - 8
	if width > 55 {
		width = 55
	}
	if width < 36 {
		width = 36
	}
	leftPad := (ts.Width - width) / 2
	topPad := ts.Height/2 - len(themes) - 2

	for i := 0; i < ts.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad

	title := " Switch Theme "
	buf.SetRow(row, padTo(leftPad)+ts.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	currentLine := ts.Theme.DimText("Current: ") + ts.Theme.AccentText(currentTheme)
	buf.SetRow(row, padTo(leftPad)+ts.Theme.DimText(BoxV+" ")+currentLine+padTo(width-len(stripANSI(currentLine))-2)+ts.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+ts.Theme.DimText(BoxV+Repeat(BoxH, width-1)+BoxV))
	row++

	for i, t := range themes {
		marker := "  "
		if i == selected {
			marker = ts.Theme.AccentText("▸ ")
		}
		radio := "  "
		if t.ID == currentTheme {
			radio = ts.Theme.SuccessText("● ")
		}

		line := ts.Theme.DimText(BoxV+" ") + marker + radio + ts.Theme.Bold(t.Name)
		desc := ts.Theme.DimText("  " + t.Description)
		full := line + desc
		padding := padTo(width - len(stripANSI(full)) - 1)
		buf.SetRow(row, padTo(leftPad)+full+padding+ts.Theme.DimText(BoxV))
		row++
	}

	for row < topPad+len(themes)+4 {
		buf.SetRow(row, padTo(leftPad)+ts.Theme.DimText(BoxV)+padTo(width-1)+ts.Theme.DimText(BoxV))
		row++
	}

	buf.SetRow(row, padTo(leftPad)+ts.Theme.DimText(BoxV+" ")+ts.Theme.DimText("↑↓ select  Enter confirm  Esc cancel")+padTo(width-len("↑↓ select  Enter confirm  Esc cancel")-2)+ts.Theme.DimText(BoxV))
	row++

	buf.SetRow(row, padTo(leftPad)+ts.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Notification Toast ───────────────────────────────────────────────────

// ToastType classifies a notification.
type ToastType int

const (
	ToastInfo ToastType = iota
	ToastSuccess
	ToastError
	ToastWarning
)

// Toast represents a single notification.
type Toast struct {
	Type    ToastType
	Message string
	TTL     int // remaining display ticks
}

// ToastManager handles notification rendering.
type ToastManager struct {
	Toasts []Toast
	Max    int
}

// AddToast appends a notification.
func (tm *ToastManager) AddToast(t ToastType, msg string, ttl int) {
	if tm.Max == 0 {
		tm.Max = 3
	}
	if len(tm.Toasts) >= tm.Max {
		tm.Toasts = tm.Toasts[1:]
	}
	tm.Toasts = append(tm.Toasts, Toast{Type: t, Message: msg, TTL: ttl})
}

// Tick decrements TTLs and removes expired toasts. Returns true if any remain.
func (tm *ToastManager) Tick() bool {
	var alive []Toast
	for _, t := range tm.Toasts {
		t.TTL--
		if t.TTL > 0 {
			alive = append(alive, t)
		}
	}
	tm.Toasts = alive
	return len(tm.Toasts) > 0
}

// RenderToasts draws toasts in the top-right corner.
func (tm *ToastManager) RenderToasts(buf *Buffer, theme Theme, width int) {
	if len(tm.Toasts) == 0 {
		return
	}

	rightCol := width - 2
	for i, t := range tm.Toasts {
		var icon string
		var colorFn func(string) string
		switch t.Type {
		case ToastSuccess:
			icon = "✓"
			colorFn = theme.SuccessText
		case ToastError:
			icon = "✗"
			colorFn = theme.ErrorText
		case ToastWarning:
			icon = "⚠"
			colorFn = theme.WarningText
		default:
			icon = "ℹ"
			colorFn = theme.AccentText
		}

		msg := fmt.Sprintf(" %s %s ", icon, Truncate(t.Message, 40))
		styled := colorFn(msg)
		startCol := rightCol - len(stripANSI(styled))
		if startCol < 0 {
			startCol = 0
		}
		buf.SetRow(i, padTo(startCol)+styled)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────
