package tui

import (
	"fmt"
	"strings"
)

// ─── Command Palette ────────────────────────────────────────────────────

// CommandPalette renders a fuzzy-filtered command picker overlay.
// Opened with Ctrl+P or "/" prefix.
type CommandPalette struct {
	Width  int
	Height int
	Theme  Theme
}

// PaletteEntry is a selectable item in the command palette.
type PaletteEntry struct {
	Category string // "command", "session", "file"
	Label    string // primary display text
	Subtitle string // secondary dim text
	Action   string // action string to return
}

// BuiltinCommands are available in the palette.
var BuiltinCommands = []PaletteEntry{
	{Category: "command", Label: "/plan", Subtitle: "Plan before executing", Action: "/plan"},
	{Category: "command", Label: "/model", Subtitle: "Switch AI model", Action: "/model"},
	{Category: "command", Label: "/memory", Subtitle: "Manage memories", Action: "/memory"},
	{Category: "command", Label: "/sessions", Subtitle: "Browse sessions", Action: "/sessions"},
	{Category: "command", Label: "/settings", Subtitle: "Open settings", Action: "/settings"},
	{Category: "command", Label: "/help", Subtitle: "Context-sensitive help", Action: "/help"},
	{Category: "command", Label: "/clear", Subtitle: "Clear screen, preserve session", Action: "/clear"},
	{Category: "command", Label: "/compact", Subtitle: "Force context compaction", Action: "/compact"},
	{Category: "command", Label: "/status", Subtitle: "Show detailed session status", Action: "/status"},
	{Category: "command", Label: "/export md", Subtitle: "Export conversation as markdown", Action: "/export md"},
	{Category: "command", Label: "/export json", Subtitle: "Export conversation as JSON", Action: "/export json"},
	{Category: "command", Label: "/cost", Subtitle: "Show detailed cost breakdown", Action: "/cost"},
}

// Render draws the command palette overlay to the buffer. The overlay
// occupies the center of the screen. Returns the rendered height.
func (cp *CommandPalette) Render(buf *Buffer, query string, entries []PaletteEntry, selected int) {
	// Filter entries by fuzzy matching query
	var filtered []PaletteEntry
	if query == "" {
		filtered = entries
	} else {
		lower := strings.ToLower(query)
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Label), lower) ||
				strings.Contains(strings.ToLower(e.Subtitle), lower) {
				filtered = append(filtered, e)
			}
		}
	}

	// Calculate size
	maxEntries := 8
	contentH := len(filtered) + 3 // header + entries + footer
	if contentH > maxEntries+3 {
		contentH = maxEntries + 3
	}
	if len(filtered) < contentH-3 {
		contentH = len(filtered) + 3
	}
	if contentH < 3 {
		contentH = 3
	}

	width := cp.Width - 8
	if width > 70 {
		width = 70
	}
	if width < 40 {
		width = 40
	}
	leftPad := (cp.Width - width) / 2
	topPad := (cp.Height - contentH) / 2
	if topPad < 1 {
		topPad = 1
	}

	// Clear the area first
	for i := 0; i < cp.Height; i++ {
		buf.SetRow(i, "")
	}

	// Render top border
	row := topPad
	title := " Command Palette "
	buf.SetRow(row, padTo(leftPad)+cp.Theme.DimText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	// Search input
	searchLine := cp.Theme.AccentText("> ") + query + cp.Theme.AccentText("█")
	if len(query) == 0 {
		searchLine = cp.Theme.DimText("> Type to search...")
	}
	buf.SetRow(row, padTo(leftPad)+cp.Theme.DimText(BoxV+" ")+searchLine+padTo(width-len(stripANSI(searchLine))-2)+cp.Theme.DimText(BoxV))
	row++

	// Separator
	buf.SetRow(row, padTo(leftPad)+cp.Theme.DimText(BoxV+Repeat(BoxH, width-1)+BoxV))
	row++

	// Entries
	currentCategory := ""
	startIdx := 0
	if selected >= contentH-3 {
		startIdx = selected - contentH + 4
	}
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + contentH - 3
	if endIdx > len(filtered) {
		endIdx = len(filtered)
	}

	for i, entry := range filtered[startIdx:endIdx] {
		idx := i + startIdx
		prefix := "  "
		if idx == selected {
			prefix = cp.Theme.AccentText("▸ ")
		}

		if entry.Category != currentCategory {
			currentCategory = entry.Category
			catHeader := cp.Theme.DimText(strings.ToUpper(currentCategory))
			buf.SetRow(row, padTo(leftPad)+cp.Theme.DimText(BoxV+" ")+catHeader+padTo(width-len(stripANSI(catHeader))-2)+cp.Theme.DimText(BoxV))
			row++
		}

		label := entry.Label
		subtitle := ""
		if entry.Subtitle != "" {
			subtitle = "  " + cp.Theme.DimText(entry.Subtitle)
		}

		line := cp.Theme.DimText(BoxV+" ") + prefix + cp.Theme.Bold(label) + subtitle
		line += padTo(width - len(stripANSI(line)) - 1)
		line += cp.Theme.DimText(BoxV)
		buf.SetRow(row, padTo(leftPad)+line)
		row++
	}

	// Fill remaining
	for row < topPad+contentH-1 {
		buf.SetRow(row, padTo(leftPad)+cp.Theme.DimText(BoxV)+padTo(width-1)+cp.Theme.DimText(BoxV))
		row++
	}

	// Bottom border
	buf.SetRow(row, padTo(leftPad)+cp.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ─── Permission Dialog ──────────────────────────────────────────────────

// PermissionDialog handles interactive permission requests. It shows a modal
// dialog allowing the user to allow/deny tool execution.
type PermissionDialog struct {
	Width  int
	Height int
	Theme  Theme
}

// PermissionChoice represents the user's decision.
type PermissionChoice int

const (
	PermAllowOnce PermissionChoice = iota
	PermAllowAll
	PermDeny
	PermShowDiff
	PermSkip
)

// PermissionRequest contains the details of a tool permission request.
type PermissionRequest struct {
	ToolName string
	Summary  string
	FilePath string
	Command  string
	Edits    string // diff or content preview
}

// RenderPermission renders the permission prompt as a modal dialog overlay.
// It fills the entire buffer with the dialog at center.
func (pd *PermissionDialog) RenderPermission(buf *Buffer, req PermissionRequest) {
	width := pd.Width - 8
	if width > 60 {
		width = 60
	}
	if width < 36 {
		width = 36
	}
	leftPad := (pd.Width - width) / 2

	contentH := 8
	if req.Edits != "" {
		contentH += minInt(6, len(strings.Split(req.Edits, "\n")))
	}
	topPad := (pd.Height - contentH) / 2
	if topPad < 1 {
		topPad = 1
	}

	// Clear
	for i := 0; i < pd.Height; i++ {
		buf.SetRow(i, "")
	}

	row := topPad

	// Header
	title := " Permission Required "
	buf.SetRow(row, padTo(leftPad)+pd.Theme.WarningText(BoxTL+title+Repeat(BoxH, width-len(title)-1)+BoxTR))
	row++

	// Tool info
	toolLine := fmt.Sprintf("[%s] wants to %s", pd.Theme.Bold(req.ToolName), req.Summary)
	buf.SetRow(row, padTo(leftPad)+pd.Theme.DimText(BoxV+" ")+toolLine+padTo(width-len(stripANSI(toolLine))-2)+pd.Theme.DimText(BoxV))
	row++

	// File path if available
	if req.FilePath != "" {
		fileLine := pd.Theme.DimText("File: ") + pd.Theme.AccentText(req.FilePath)
		buf.SetRow(row, padTo(leftPad)+pd.Theme.DimText(BoxV+" ")+fileLine+padTo(width-len(stripANSI(fileLine))-2)+pd.Theme.DimText(BoxV))
		row++
	}

	// Diff preview
	if req.Edits != "" {
		editLines := strings.Split(req.Edits, "\n")
		for _, l := range editLines[:minInt(6, len(editLines))] {
			editLine := pd.Theme.DimText("  ") + l
			buf.SetRow(row, padTo(leftPad)+pd.Theme.DimText(BoxV+" ")+editLine+padTo(width-len(stripANSI(editLine))-2)+pd.Theme.DimText(BoxV))
			row++
		}
	}

	// Separator
	buf.SetRow(row, padTo(leftPad)+pd.Theme.DimText(BoxV+Repeat(BoxH, width-1)+BoxV))
	row++

	// Choices
	choices := pd.Theme.Bold("[Y] Allow once  [A] Allow all edits  [N] Deny  [S] Show diff")
	buf.SetRow(row, padTo(leftPad)+pd.Theme.DimText(BoxV+" ")+choices+padTo(width-len(stripANSI(choices))-2)+pd.Theme.DimText(BoxV))
	row++

	// Bottom border
	buf.SetRow(row, padTo(leftPad)+pd.Theme.DimText(BoxBL+Repeat(BoxH, width-1)+BoxBR))
}

// ParsePermissionChoice maps keystrokes to choices.
func ParsePermissionChoice(key string) (PermissionChoice, bool) {
	switch strings.ToLower(key) {
	case "y", "enter":
		return PermAllowOnce, true
	case "a":
		return PermAllowAll, true
	case "n":
		return PermDeny, true
	case "d", "s":
		return PermShowDiff, true
	default:
		return PermDeny, false
	}
}

// ─── Key Matcher for Palette ────────────────────────────────────────────

// MatchKey checks if a key input matches a palette entry for quick-select.
func MatchKey(key string, entries []PaletteEntry, selected int) (string, bool) {
	// Check if it's a navigation key handled by caller
	if len(key) == 1 {
		lower := strings.ToLower(key)
		for _, e := range entries {
			if strings.HasPrefix(strings.ToLower(e.Label), "/"+lower) {
				return e.Action, true
			}
		}
	}
	return "", false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
