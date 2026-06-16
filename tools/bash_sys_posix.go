//go:build !windows
// +build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func setupProcessOptions(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// taskTerminateTree asks a process group to exit gracefully by signalling
// every member with SIGTERM. The caller is expected to have placed the
// process in its own group via setupProcessOptions (Setpgid). SIGTERM is
// catchable so scripts can run cleanup, but untouched members still die by
// default. Errors are deliberately ignored — failing soft is fine, the
// caller will escalate to SIGKILL after the grace period.
func taskTerminateTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}

// taskKillTree force-kills a process group. SIGKILL cannot be caught,
// blocked, or ignored, so this is the unblockable fallback used after the
// SIGTERM grace period has elapsed.
func taskKillTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

