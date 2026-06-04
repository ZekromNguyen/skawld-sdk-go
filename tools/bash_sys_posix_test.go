//go:build !windows
// +build !windows

package tools

import (
	"os/exec"
	"testing"
)

func TestBashSetupProcessOptionsCreatesProcessGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "echo ok")
	setupProcessOptions(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("expected Bash process options to create a process group")
	}
}
