package tui

import (
	"fmt"
	"os"
)

// Screen manages the terminal: raw mode, alternate screen, cursor control, and
// resize signals. It writes to the provided Writer (usually os.Stdout).
type Screen struct {
	out       *os.File
	origMode  uint32 // Windows: original console mode
	rawMode   bool
	altScreen bool
	Theme     Theme
	Width     int
	Height    int

	// Resize receives terminal resize events.
	Resize chan struct{}
}

// NewScreen creates a screen attached to stdout.
func NewScreen() (*Screen, error) {
	s := &Screen{
		out:    os.Stdout,
		Theme:  DefaultTheme(),
		Resize: make(chan struct{}, 8),
	}
	w, h, err := s.terminalSize()
	if err != nil {
		w, h = 80, 24
	}
	s.Width = w
	s.Height = h

	// Check NO_COLOR
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		s.Theme = NoColorTheme()
	}

	return s, nil
}

// EnterAltScreen switches to the alternative screen buffer.
func (s *Screen) EnterAltScreen() {
	if s.altScreen {
		return
	}
	s.altScreen = true
	fmt.Fprint(s.out, CursorHide())
	fmt.Fprint(s.out, EnterAltScreen())
	fmt.Fprint(s.out, ClearScreen())
}

// ExitAltScreen restores the main screen buffer.
func (s *Screen) ExitAltScreen() {
	if !s.altScreen {
		return
	}
	s.altScreen = false
	fmt.Fprint(s.out, CursorShow())
	fmt.Fprint(s.out, ExitAltScreen())
}

// Clear clears the screen.
func (s *Screen) Clear() {
	fmt.Fprint(s.out, ClearScreen())
}

// Write writes raw output to the terminal.
func (s *Screen) Write(data string) {
	fmt.Fprint(s.out, data)
}

// WriteAt writes data at a specific cursor position (1-indexed).
func (s *Screen) WriteAt(row, col int, data string) {
	fmt.Fprint(s.out, CursorMove(row, col))
	fmt.Fprint(s.out, ClearToEndOfLine())
	fmt.Fprint(s.out, data)
}

// UpdateSize refreshes the terminal dimensions.
func (s *Screen) UpdateSize() {
	w, h, err := s.terminalSize()
	if err != nil {
		return
	}
	if w != s.Width || h != s.Height {
		s.Width = w
		s.Height = h
		select {
		case s.Resize <- struct{}{}:
		default:
		}
	}
}

// Flush ensures all pending output is written.
func (s *Screen) Flush() {
	_ = s.out.Sync()
}

// Reset releases all terminal resources and restores normal mode.
func (s *Screen) Reset() {
	s.ExitAltScreen()
	_ = s.ExitRawMode()
	s.Flush()
}
