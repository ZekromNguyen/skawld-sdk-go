//go:build windows
// +build windows

package tools

import (
	"os/exec"
	"testing"
)

func TestBashSetupProcessOptionsHidesWindow(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "echo ok")
	setupProcessOptions(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("expected Bash process options to hide Windows shell window")
	}
}
