package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// ─── ANSI Escape Sequence Constants ───────────────────────────────────

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiItalic  = "\033[3m"
	ansiUnder   = "\033[4m"
	ansiReverse = "\033[7m"
)

// ─── Cursor Control ────────────────────────────────────────────────────

// CursorMove moves the cursor to (row, col) — 1-indexed.
func CursorMove(row, col int) string {
	return fmt.Sprintf("\033[%d;%dH", row, col)
}

// CursorUp moves the cursor up n lines.
func CursorUp(n int) string { return fmt.Sprintf("\033[%dA", n) }

// CursorDown moves the cursor down n lines.
func CursorDown(n int) string { return fmt.Sprintf("\033[%dB", n) }

// CursorForward moves the cursor right n columns.
func CursorForward(n int) string { return fmt.Sprintf("\033[%dC", n) }

// CursorBack moves the cursor left n columns.
func CursorBack(n int) string { return fmt.Sprintf("\033[%dD", n) }

// CursorHide hides the cursor.
func CursorHide() string { return "\033[?25l" }

// CursorShow shows the cursor.
func CursorShow() string { return "\033[?25h" }

// CursorSave saves the cursor position.
func CursorSave() string { return "\033[s" }

// CursorRestore restores the cursor position.
func CursorRestore() string { return "\033[u" }

// ─── Screen Control ────────────────────────────────────────────────────

// EnterAltScreen switches to the alternate screen buffer.
func EnterAltScreen() string { return "\033[?1049h" }

// ExitAltScreen switches back to the main screen buffer.
func ExitAltScreen() string { return "\033[?1049l" }

// ClearScreen clears the entire screen and moves cursor to home.
func ClearScreen() string { return "\033[2J\033[H" }

// ClearLine clears the current line.
func ClearLine() string { return "\033[2K" }

// ClearToEndOfLine clears from cursor to end of line.
func ClearToEndOfLine() string { return "\033[0K" }

// EnterBracketedPaste enables bracketed paste mode.
func EnterBracketedPaste() string { return "\033[?2004h" }

// ExitBracketedPaste disables bracketed paste mode.
func ExitBracketedPaste() string { return "\033[?2004l" }

// SearchPrompt returns the reverse-i-search prompt.
func SearchPrompt(query string) string {
	return fmt.Sprintf("\r\033[2K(bck-i-search)`%s': ", query)
}

// ScrollUp scrolls the screen up n lines.
func ScrollUp(n int) string { return fmt.Sprintf("\033[%dS", n) }

// ScrollDown scrolls the screen down n lines.
func ScrollDown(n int) string { return fmt.Sprintf("\033[%dT", n) }

// ─── Color Escape Generation ───────────────────────────────────────────

func ansiFg256(code int) string {
	if code < 0 || code > 255 {
		return ""
	}
	return fmt.Sprintf("\033[38;5;%dm", code)
}

func ansiBg256(code int) string {
	if code < 0 || code > 255 {
		return ""
	}
	return fmt.Sprintf("\033[48;5;%dm", code)
}

func ansiBoldFg256(code int) string {
	if code < 0 || code > 255 {
		return ""
	}
	return fmt.Sprintf("\033[1;38;5;%dm", code)
}

func ansiDimFg256(code int) string {
	if code < 0 || code > 255 {
		return ""
	}
	return fmt.Sprintf("\033[2;38;5;%dm", code)
}

// RGBFg returns an ANSI true-color foreground escape.
func RGBFg(r, g, b uint8) string {
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

// RGBBg returns an ANSI true-color background escape.
func RGBBg(r, g, b uint8) string {
	return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
}

// ─── Box Drawing ────────────────────────────────────────────────────────

// Box characters — using Unicode box drawing.
const (
	BoxH      = "─"
	BoxV      = "│"
	BoxTL     = "╭"
	BoxTR     = "╮"
	BoxBL     = "╰"
	BoxBR     = "╯"
	BoxCross  = "┼"
	BoxTDown  = "┬"
	BoxTUp    = "┴"
	BoxTLeft  = "┤"
	BoxTRight = "├"
)

// BoxDraw draws a bordered box with the given title and content lines.
// width is the total width including borders.
func BoxDraw(title string, lines []string, width int) string {
	if width < 4 {
		width = 80
	}
	var b strings.Builder

	// Top border with optional title
	b.WriteString(BoxTL)
	inner := width - 2
	if title != "" {
		titlePart := " " + title + " "
		b.WriteString(titlePart)
		remain := inner - len(titlePart)
		if remain > 0 {
			b.WriteString(strings.Repeat(BoxH, remain))
		}
	} else {
		b.WriteString(strings.Repeat(BoxH, inner))
	}
	b.WriteString(BoxTR)
	b.WriteByte('\n')

	// Content lines
	for _, line := range lines {
		b.WriteString(BoxV)
		padded := padRight(line, inner)
		b.WriteString(padded)
		b.WriteString(BoxV)
		b.WriteByte('\n')
	}

	// Bottom border
	b.WriteString(BoxBL)
	b.WriteString(strings.Repeat(BoxH, inner))
	b.WriteString(BoxBR)
	return b.String()
}

// ─── Progress Bar ───────────────────────────────────────────────────────

// ProgressBar renders a progress bar given a fill ratio (0.0–1.0).
func ProgressBar(fill float64, width int, t Theme) string {
	if width <= 0 {
		width = 20
	}
	if fill < 0 {
		fill = 0
	}
	if fill > 1 {
		fill = 1
	}

	filled := int(fill * float64(width))
	barColor := t.Accent
	if fill > 0.5 {
		barColor = t.Warning
	}
	if fill > 0.8 {
		barColor = t.Error
	}

	var b strings.Builder
	b.WriteString(t.Styled(strings.Repeat("█", filled), barColor, false, false))
	b.WriteString(t.DimText(strings.Repeat("░", width-filled)))
	b.WriteString(t.DimText(fmt.Sprintf(" %d%%", int(fill*100))))

	return b.String()
}

// ProgressBarRatio renders a progress bar from current/total counts into width
// characters. Returns only the bar (no label); callers add context.
func ProgressBarRatio(current, total, width int) string {
	if width <= 0 {
		width = 20
	}
	if total <= 0 {
		total = 1
	}
	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}

	filled := current * width / total
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat(" ", width-filled)
	return bar
}

// ─── Spinner ────────────────────────────────────────────────────────────

// Spinner frames for indeterminate progress.
var SpinnerFrames = []string{"◌", "○", "◯", "◎", "●", "◉", "◯", "○"}

// spinnerColors maps spinner frames to colors based on position.
var spinnerColors = []Color{MutedTeal, MutedTeal, ElectricBlue, ElectricBlue, ElectricBlue, ElectricBlue, MutedTeal, MutedTeal}

// Spinner returns the spinner character and color for the given tick.
func Spinner(tick int, t Theme) string {
	frame := SpinnerFrames[tick%len(SpinnerFrames)]
	c := spinnerColors[tick%len(spinnerColors)]
	return t.Styled(frame, c, false, false)
}

// ─── Helpers ────────────────────────────────────────────────────────────

// padRight pads a string to the given width, truncating if longer.
func padRight(s string, width int) string {
	runes := []rune(strconv.Quote(s)) // placeholder, not actually used
	_ = runes

	// Use simple string length — ok for ASCII content.
	if len(s) > width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

// Truncate truncates a string to maxLen, adding "…" if truncated.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return ""
	}
	return s[:maxLen-1] + "…"
}

// WrapText wraps text to the given width, preserving words when possible.
func WrapText(text string, width int) []string {
	if width <= 0 {
		width = 80
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		for len(paragraph) > width {
			// Find a word boundary to break at
			br := width
			for br > 0 && paragraph[br] != ' ' {
				br--
			}
			if br == 0 {
				// No space found — hard break
				br = width
			}
			lines = append(lines, paragraph[:br])
			paragraph = strings.TrimSpace(paragraph[br:])
		}
		if paragraph != "" {
			lines = append(lines, paragraph)
		}
	}
	return lines
}

// DurationFormat formats a millisecond duration as human-readable.
func DurationFormat(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
	}
	return fmt.Sprintf("%dm%ds", ms/60000, (ms%60000)/1000)
}

// BytesFormat formats a byte count as human-readable.
func BytesFormat(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
}

// TokenFormat formats a token count as human-readable.
func TokenFormat(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1000000)
}

// CostFormat formats a cost in dollars.
func CostFormat(dollars float64) string {
	if dollars < 0.01 {
		return "<$0.01"
	}
	if dollars < 1 {
		return fmt.Sprintf("$%.2f", dollars)
	}
	return fmt.Sprintf("$%.2f", dollars)
}

// Indent prepends indentStr to each line.
func Indent(text string, indentStr string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = indentStr + line
	}
	return strings.Join(lines, "\n")
}

// Repeat returns s repeated n times.
func Repeat(s string, n int) string {
	return strings.Repeat(s, n)
}
