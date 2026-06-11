package tui

import (
	"fmt"
	"strings"
)

// DiffLine represents one line of a unified diff.
type DiffLine struct {
	Kind    rune   // '+', '-', ' ', '@'
	Content string
	OldNum  int // 1-indexed, 0 if header
	NewNum  int
}

// UnifiedDiff computes a simple line-based unified diff between old and new.
// Returns at most maxLines diff lines (excluding header).
func UnifiedDiff(old, new string, oldLabel, newLabel string, maxLines int) []DiffLine {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	var result []DiffLine

	// Header
	result = append(result, DiffLine{Kind: '@', Content: fmt.Sprintf("── %s → %s", oldLabel, newLabel)})

	// Simple LCS-based diff (optimized for small edits — the common case)
	edits := computeLineEdits(oldLines, newLines)

	for _, e := range edits {
		switch e.Kind {
		case ' ':
			result = append(result, DiffLine{Kind: ' ', Content: e.Content, OldNum: e.OldLine, NewNum: e.NewLine})
		case '-':
			result = append(result, DiffLine{Kind: '-', Content: e.Content, OldNum: e.OldLine})
		case '+':
			result = append(result, DiffLine{Kind: '+', Content: e.Content, NewNum: e.NewLine})
		}
	}

	if maxLines > 0 && len(result) > maxLines {
		result = result[:maxLines]
		result = append(result, DiffLine{Kind: '@', Content: fmt.Sprintf("  ... %d more lines", len(result)-maxLines)})
	}

	return result
}

type lineEdit struct {
	Kind    rune
	Content string
	OldLine int
	NewLine int
}

// computeLineEdits uses a simplified Myers-like algorithm for line diffing.
// For the TUI use case (small edits), this is sufficient.
func computeLineEdits(old, new []string) []lineEdit {
	// Build LCS table (optimized for small diffs — the common case for Edit tool)
	m, n := len(old), len(new)

	// For very small inputs, just do a simple diff
	if m == 0 && n == 0 {
		return nil
	}
	if m == 0 {
		var edits []lineEdit
		for i, line := range new {
			edits = append(edits, lineEdit{Kind: '+', Content: line, NewLine: i + 1})
		}
		return edits
	}
	if n == 0 {
		var edits []lineEdit
		for i, line := range old {
			edits = append(edits, lineEdit{Kind: '-', Content: line, OldLine: i + 1})
		}
		return edits
	}

	// Build LCS table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else {
				if lcs[i-1][j] > lcs[i][j-1] {
					lcs[i][j] = lcs[i-1][j]
				} else {
					lcs[i][j] = lcs[i][j-1]
				}
			}
		}
	}

	// Backtrack to produce edits
	var edits []lineEdit
	i, j := m, n
	var rev []lineEdit
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && old[i-1] == new[j-1] {
			rev = append(rev, lineEdit{Kind: ' ', Content: old[i-1], OldLine: i, NewLine: j})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			rev = append(rev, lineEdit{Kind: '+', Content: new[j-1], NewLine: j})
			j--
		} else if i > 0 {
			rev = append(rev, lineEdit{Kind: '-', Content: old[i-1], OldLine: i})
			i--
		}
	}
	for k := len(rev) - 1; k >= 0; k-- {
		edits = append(edits, rev[k])
	}

	// Collapse unchanged context — show only 3 lines of context around changes
	return collapseContext(edits, 3)
}

// collapseContext shows only limited lines of unchanged context around diffs.
func collapseContext(edits []lineEdit, contextLines int) []lineEdit {
	var result []lineEdit
	var contextBuf []lineEdit
	hasChanges := false

	flushContext := func() {
		if !hasChanges && len(contextBuf) > contextLines {
			// Show only last N context lines before changes
			if len(result) > 0 {
				result = append(result, lineEdit{Kind: '@', Content: "  ..."})
			}
			result = append(result, contextBuf[len(contextBuf)-contextLines:]...)
		} else {
			result = append(result, contextBuf...)
		}
		contextBuf = nil
		hasChanges = false
	}

	for _, e := range edits {
		if e.Kind == ' ' {
			contextBuf = append(contextBuf, e)
		} else {
			flushContext()
			hasChanges = true
			result = append(result, e)
		}
	}
	flushContext()
	return result
}

// EditSummary computes a human-readable summary of edit changes.
func EditSummary(old, new string) string {
	oldLines := strings.Split(strings.TrimSpace(old), "\n")
	newLines := strings.Split(strings.TrimSpace(new), "\n")
	added := len(newLines) - len(oldLines)
	if added > 0 {
		return fmt.Sprintf("+%d lines", added)
	}
	if added < 0 {
		return fmt.Sprintf("-%d lines", -added)
	}
	return "modified"
}

// RenderDiffLine renders a single diff line with appropriate color.
func RenderDiffLine(dl DiffLine, t Theme) string {
	switch dl.Kind {
	case '+':
		return t.SuccessText("+") + " " + t.SuccessText(dl.Content)
	case '-':
		return t.ErrorText("-") + " " + t.ErrorText(dl.Content)
	case '@':
		return t.DimText("  " + dl.Content)
	default:
		return "  " + dl.Content
	}
}

// TruncateDiff trims a diff to fit within maxLines.
func TruncateDiff(lines []DiffLine, maxLines int) []DiffLine {
	if len(lines) <= maxLines {
		return lines
	}
	hdr := lines[0]
	body := lines[1:]
	trimmed := body[:maxLines-2]
	return append([]DiffLine{hdr}, append(trimmed, DiffLine{Kind: '@', Content: fmt.Sprintf("  ... and %d more lines", len(body)-maxLines+2)})...)
}

// ComputeSimpleDiff computes a quick diff between old and new strings without
// the full LCS — useful for the Write tool where we don't have old content.
func ComputeSimpleDiff(old, new string) string {
	if old == "" {
		return "+ " + strings.ReplaceAll(strings.TrimSpace(new), "\n", "\n+ ")
	}
	dl := UnifiedDiff(old, new, "before", "after", 20)
	var sb strings.Builder
	for _, l := range dl {
		switch l.Kind {
		case '+':
			sb.WriteString("+ " + l.Content + "\n")
		case '-':
			sb.WriteString("- " + l.Content + "\n")
		case '@':
			sb.WriteString("  " + l.Content + "\n")
		default:
			sb.WriteString("  " + l.Content + "\n")
		}
	}
	return strings.TrimSpace(sb.String())
}