//go:build windows

package tui

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

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

// HandleSignals sets up SIGWINCH and SIGINT handling.
func (s *Screen) HandleSignals() chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// On Windows, we poll for resize via a separate mechanism
	go s.pollResize()
	return sigCh
}

func (s *Screen) pollResize() {
	// Poll terminal size periodically; Windows doesn't send SIGWINCH.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.UpdateSize()
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
