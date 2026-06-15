package tui

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Screen manages the terminal: raw mode, alternate screen, cursor control, and
// resize signals. It writes to the provided Writer (usually os.Stdout).
type Screen struct {
	out       *os.File
	origMode  uint32
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

type windowSize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// EnterRawMode enables raw terminal mode.
func (s *Screen) EnterRawMode() error {
	if s.rawMode {
		return nil
	}
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode); err != nil {
		return fmt.Errorf("get console mode: %w", err)
	}
	s.origMode = mode
	raw := mode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT |
		windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_WINDOW_INPUT)
	raw |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	if err := windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), raw); err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}

	// Enable virtual terminal processing on stdout for ANSI escape support.
	var outMode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &outMode); err != nil {
		// best effort
	} else {
		outMode |= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
		_ = windows.SetConsoleMode(windows.Handle(os.Stdout.Fd()), outMode)
	}

	s.rawMode = true

	// Enable bracketed paste mode so pasted text comes as a single block.
	fmt.Fprint(s.out, EnterBracketedPaste())

	return nil
}

// ExitRawMode restores the original console mode.
func (s *Screen) ExitRawMode() error {
	if !s.rawMode {
		return nil
	}
	// Disable bracketed paste before restoring console mode.
	fmt.Fprint(s.out, ExitBracketedPaste())
	s.rawMode = false
	return windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), s.origMode)
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

// ─── Signal Handling ────────────────────────────────────────────────────

// HandleSignals sets up SIGWINCH and SIGINT handling.
func (s *Screen) HandleSignals() chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// On Windows, we poll for resize via a separate mechanism
	go s.pollResize()
	return sigCh
}

func (s *Screen) pollResize() {
	// Poll terminal size periodically — Windows doesn't send SIGWINCH.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.UpdateSize()
	}
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

// terminalSize queries the terminal dimensions via Windows API.
func (s *Screen) terminalSize() (int, int, error) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
		return 80, 24, nil
	}
	w := int(info.Window.Right - info.Window.Left + 1)
	h := int(info.Window.Bottom - info.Window.Top + 1)
	return w, h, nil
}

// ─── Flush ──────────────────────────────────────────────────────────────

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
