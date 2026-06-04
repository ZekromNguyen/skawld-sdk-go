//go:build windows
// +build windows

package tools

import (
	"os/exec"
	"syscall"
)

func setupProcessOptions(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
