package tui

import (
	"bytes"
	"fmt"
	"strings"
)

// Buffer implements double-buffered terminal output. Rows are assembled in
// memory, then diffed against the previous frame to minimize writes.
type Buffer struct {
	Rows    []string
	Width   int
	Height  int
	prev    []string
	changed []bool
}

// NewBuffer creates a write buffer sized for the terminal.
func NewBuffer(width, height int) *Buffer {
	b := &Buffer{
		Width:  width,
		Height: height,
	}
	b.Reset()
	return b
}

// Reset clears the buffer and re-initializes to empty rows.
func (b *Buffer) Reset() {
	b.Rows = make([]string, b.Height)
	for i := range b.Rows {
		b.Rows[i] = ""
	}
	b.prev = make([]string, b.Height)
	b.changed = make([]bool, b.Height)
}

// SetRow sets a specific row (0-indexed) in the buffer.
func (b *Buffer) SetRow(row int, content string) {
	if row < 0 || row >= b.Height {
		return
	}
	b.Rows[row] = content
}

// WriteLine writes a line at the next available row.
func (b *Buffer) WriteLine(row int, content string) {
	b.SetRow(row, content)
}

// WriteStyled writes styled content at a specific position.
func (b *Buffer) WriteStyled(row, col int, content string) string {
	padded := padTo(col) + content
	if row >= 0 && row < b.Height {
		b.Rows[row] = padded
	}
	return padded
}

// Fill fills the remaining rows from startRow with empty lines.
func (b *Buffer) Fill(startRow int) {
	for i := startRow; i < b.Height; i++ {
		b.Rows[i] = ""
	}
}

// Render diffs the buffer against the previous frame and writes only changed
// rows to the screen. Returns the full rendered output as a string.
func (b *Buffer) Render(screen *Screen) {
	for i := 0; i < b.Height; i++ {
		row := b.Rows[i]
		// Truncate to terminal width
		if len(row) > b.Width {
			row = row[:b.Width]
		}
		changed := row != b.prev[i]
		b.changed[i] = changed

		if changed {
			screen.WriteAt(i+1, 1, row)
		}

		b.prev[i] = row
	}
	screen.Flush()
}

// FullRender renders all rows unconditionally.
func (b *Buffer) FullRender(screen *Screen) {
	for i := 0; i < b.Height; i++ {
		row := b.Rows[i]
		if len(row) > b.Width {
			row = row[:b.Width]
		}
		screen.WriteAt(i+1, 1, row)
		b.prev[i] = row
	}
	screen.Flush()
}

// String returns the buffer contents as a single string, useful for testing.
func (b *Buffer) String() string {
	return strings.Join(b.Rows, "\n")
}

// ─── Diffing Utilities ──────────────────────────────────────────────────

// Diff computes the minimal set of changes between two buffers.
func Diff(prev, curr []string) []DiffOp {
	var ops []DiffOp
	max := len(prev)
	if len(curr) > max {
		max = len(curr)
	}
	for i := 0; i < max; i++ {
		var p, c string
		if i < len(prev) {
			p = prev[i]
		}
		if i < len(curr) {
			c = curr[i]
		}
		if p != c {
			ops = append(ops, DiffOp{Row: i + 1, Content: c})
		}
	}
	return ops
}

// DiffOp represents a single row change.
type DiffOp struct {
	Row     int
	Content string
}

// Apply applies diff operations to the screen.
func (d DiffOp) Apply(s *Screen) {
	s.WriteAt(d.Row, 1, d.Content)
}

// ─── Layout Helpers ─────────────────────────────────────────────────────

// Layout represents a rectangular region in the terminal.
type Layout struct {
	Row, Col int
	Width    int
	Height   int
}

// WriteToLayout writes content into a layout, clipping to bounds.
func WriteToLayout(buf *Buffer, layout Layout, content string, rowOffset int) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		r := layout.Row + i - rowOffset
		if r < layout.Row || r >= layout.Row+layout.Height {
			continue
		}
		clipped := line
		if len(clipped) > layout.Width {
			clipped = clipped[:layout.Width]
		}
		buf.SetRow(r, padTo(layout.Col-1)+clipped)
	}
}

// ─── Content Builder ────────────────────────────────────────────────────

// ContentBuilder efficiently builds a single row of styled content.
type ContentBuilder struct {
	buf bytes.Buffer
}

// NewContentBuilder creates a content builder.
func NewContentBuilder() *ContentBuilder {
	return &ContentBuilder{}
}

// Write appends raw text.
func (c *ContentBuilder) Write(s string) {
	c.buf.WriteString(s)
}

// WriteStyled appends styled text.
func (c *ContentBuilder) WriteStyled(s string, color Color, bold, dim bool) {
	c.buf.WriteString(DefaultTheme().AnsiStart(color, bold, dim))
	c.buf.WriteString(s)
	c.buf.WriteString(DefaultTheme().AnsiReset())
}

// String returns the accumulated content.
func (c *ContentBuilder) String() string {
	return c.buf.String()
}

// Reset clears the builder.
func (c *ContentBuilder) Reset() {
	c.buf.Reset()
}

func padTo(col int) string {
	if col <= 0 {
		return ""
	}
	return fmt.Sprintf("\033[%dC", col)
}

// PadTo is the exported version of padTo for use outside the tui package.
func PadTo(col int) string {
	return padTo(col)
}
