//go:build windows
// +build windows

package tools

import (
	"fmt"
	"os/exec"
	"syscall"
)

func setupProcessOptions(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}

// taskTerminateTree on Windows uses taskkill /T (without /F) so child
// processes get a chance to run cleanup before the parent is escalated.
func taskTerminateTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/pid", fmt.Sprint(pid), "/T").Run()
}

// taskKillTree on Windows uses taskkill /F /T (force, including children).
func taskKillTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/pid", fmt.Sprint(pid), "/T", "/F").Run()
}
