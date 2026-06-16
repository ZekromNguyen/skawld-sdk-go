//go:build !windows

package tui

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

var savedTermState *term.State

// EnterRawMode enables raw terminal mode.
func (s *Screen) EnterRawMode() error {
	if s.rawMode {
		return nil
	}
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("make raw: %w", err)
	}
	savedTermState = state
	s.rawMode = true
	fmt.Fprint(s.out, EnterBracketedPaste())
	return nil
}

// ExitRawMode restores the original terminal mode.
func (s *Screen) ExitRawMode() error {
	if !s.rawMode {
		return nil
	}
	fmt.Fprint(s.out, ExitBracketedPaste())
	s.rawMode = false
	if savedTermState == nil {
		return nil
	}
	return term.Restore(int(os.Stdin.Fd()), savedTermState)
}

// HandleSignals sets up SIGWINCH and SIGINT handling.
func (s *Screen) HandleSignals() chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	resizeCh := make(chan os.Signal, 1)
	signal.Notify(resizeCh, syscall.SIGWINCH)
	go func() {
		for range resizeCh {
			s.UpdateSize()
		}
	}()

	return sigCh
}

// terminalSize queries the terminal dimensions via TIOCGWINSZ.
func (s *Screen) terminalSize() (int, int, error) {
	w, h, err := term.GetSize(int(s.out.Fd()))
	if err != nil {
		return 80, 24, nil
	}
	return w, h, nil
}
