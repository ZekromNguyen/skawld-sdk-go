package skawld_test

import (
	"os/exec"
	"testing"
)

func TestExamplesBuild(t *testing.T) {
	cmd := exec.Command("go", "test", "./examples/...")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("examples failed to build: %v\n%s", err, out)
	}
}
