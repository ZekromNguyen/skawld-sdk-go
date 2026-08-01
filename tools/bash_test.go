package tools

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestBashToolValidate(t *testing.T) {
	tool := BashTool{}

	// Valid
	input, err := tool.Validate(map[string]interface{}{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if input["command"] != "echo hello" {
		t.Errorf("expected echo hello")
	}
	if input["timeout_ms"] != 120000 {
		t.Errorf("expected 120000 timeout")
	}

	// Missing command
	_, err = tool.Validate(map[string]interface{}{})
	if err == nil {
		t.Errorf("expected error for missing command")
	}

	// Empty command
	_, err = tool.Validate(map[string]interface{}{"command": "   "})
	if err == nil {
		t.Errorf("expected error for empty command")
	}

	// Clamp min
	input, _ = tool.Validate(map[string]interface{}{"command": "echo", "timeout_ms": 0})
	if input["timeout_ms"] != 100 {
		t.Errorf("expected 100")
	}

	// Clamp max
	input, _ = tool.Validate(map[string]interface{}{"command": "echo", "timeout_ms": 9999999})
	if input["timeout_ms"] != 1800000 {
		t.Errorf("expected 1800000")
	}

	// Coerce string
	input, _ = tool.Validate(map[string]interface{}{"command": "echo", "timeout_ms": "5000"})
	if input["timeout_ms"] != 5000 {
		t.Errorf("expected 5000")
	}
}

func TestBashToolSummarize(t *testing.T) {
	tool := BashTool{}

	input, _ := tool.Validate(map[string]interface{}{"command": "echo hi", "description": "say hi"})
	if tool.Summarize(input) != "say hi" {
		t.Errorf("expected say hi")
	}

	input, _ = tool.Validate(map[string]interface{}{"command": "echo hello"})
	if tool.Summarize(input) != "Bash: echo hello" {
		t.Errorf("expected Bash: echo hello")
	}

	long := strings.Repeat("a", 80)
	input, _ = tool.Validate(map[string]interface{}{"command": long})
	sum := tool.Summarize(input)
	if !strings.HasPrefix(sum, "Bash: ") || !strings.HasSuffix(sum, "…") || len([]rune(sum)) != 67 {
		t.Errorf("expected truncated summary, got %v", sum)
	}
}

func TestBashToolExecuteSuccess(t *testing.T) {
	tool := BashTool{}
	ctx := makeCtx(os.TempDir())

	// echo
	input, _ := tool.Validate(map[string]interface{}{"command": "echo hello"})
	res, err := tool.Execute(input, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %v", res.Content)
	}
	content := res.Content.(string)
	if !strings.Contains(content, "hello") || !strings.HasSuffix(strings.TrimSpace(content), "exit: 0") {
		t.Errorf("unexpected output: %v", content)
	}
}

func TestBashToolExecuteFalse(t *testing.T) {
	tool := BashTool{}
	ctx := makeCtx(os.TempDir())

	input, _ := tool.Validate(map[string]interface{}{"command": "exit 1"})
	res, _ := tool.Execute(input, ctx)
	if res.IsError {
		t.Errorf("expected is_error false for non-zero exit code")
	}
	if !strings.HasSuffix(strings.TrimSpace(res.Content.(string)), "exit: 1") {
		t.Errorf("expected exit: 1, got %v", res.Content)
	}
}

func TestBashToolExecuteTimeout(t *testing.T) {
	tool := BashTool{}
	ctx := makeCtx(os.TempDir())

	// sleep isn't natively on windows cmd either. But powershell has Start-Sleep.
	// Let's use powershell -c start-sleep 5 on windows or sleep 5
	cmd := "sleep 5"
	if os.PathSeparator == '\\' {
		cmd = "ping 127.0.0.1 -n 6 > nul"
	}
	input, _ := tool.Validate(map[string]interface{}{"command": cmd, "timeout_ms": 200})
	res, _ := tool.Execute(input, ctx)

	if !res.IsError {
		t.Errorf("expected error on timeout")
	}
	if !strings.Contains(res.Content.(string), "timed out") {
		t.Errorf("expected timeout message, got %v", res.Content)
	}
}

func TestBashToolExecuteAbort(t *testing.T) {
	tool := BashTool{}

	runCtx, cancel := context.WithCancel(context.Background())
	ctx := core.ToolContext{
		CWD:     os.TempDir(),
		Context: runCtx,
	}

	cmd := "sleep 5"
	if os.PathSeparator == '\\' {
		cmd = "powershell -c \"Start-Sleep -Seconds 5\""
	}

	input, _ := tool.Validate(map[string]interface{}{"command": cmd})

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	res, _ := tool.Execute(input, ctx)
	if !res.IsError || !strings.Contains(res.Content.(string), "aborted") {
		t.Errorf("expected aborted message, got %v", res.Content)
	}
}

func TestTerminateProcessTreeWaitsForProcessExit(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("process-group shell semantics differ on windows")
	}
	// The shell traps SIGTERM and exits 0, so terminateProcessTree should
	// succeed via the graceful path without ever escalating to SIGKILL.
	// Using `sleep 10` here previously led to flakiness on heavily loaded
	// CI runners — the shell would be killed before its child, leaving
	// waitid() blocked on a reaped-but-not-finished process group.
	cmd := exec.Command("sh", "-c", "trap 'exit 0' TERM; echo ready; while true; do sleep 0.05; done")
	setupProcessOptions(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if ready, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || ready != "ready\n" {
		t.Fatalf("wait for shell signal handler: ready=%q err=%v", ready, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Hard outer deadline: terminateProcessTree is contracted to return
	// within 2*grace plus scheduler jitter. We give it 5s as a generous
	// safety net so a regression fails fast instead of tripling CI time.
	hardDeadline := time.After(5 * time.Second)
	errCh := make(chan error, 1)
	go func() {
		errCh <- terminateProcessTree(cmd, done, time.Second)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected graceful termination, got %v", err)
		}
	case <-hardDeadline:
		t.Fatal("terminateProcessTree did not return within 5s; possible regression in process-tree cleanup")
	}

	select {
	case <-done:
		t.Fatal("wait channel should have been drained by terminateProcessTree")
	default:
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("expected process state to be exited with status 0, got %s", cmd.ProcessState.String())
	}
}

func TestBashToolExecutePreAborted(t *testing.T) {
	tool := BashTool{}
	runCtx, cancel := context.WithCancel(context.Background())
	cancel() // abort immediately

	ctx := core.ToolContext{
		CWD:     os.TempDir(),
		Context: runCtx,
	}
	input, _ := tool.Validate(map[string]interface{}{"command": "echo hi"})
	res, _ := tool.Execute(input, ctx)

	if !res.IsError || !strings.Contains(res.Content.(string), "aborted") {
		t.Errorf("expected aborted message immediately, got %v", res.Content)
	}
}

func TestBashToolOutputTruncation(t *testing.T) {
	tool := BashTool{}
	ctx := makeCtx(os.TempDir())

	cmd := "yes aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa | head -c 60000"
	if os.PathSeparator == '\\' {
		t.Skip("skip large output generation on windows for simplicity")
	}
	input, _ := tool.Validate(map[string]interface{}{"command": cmd, "timeout_ms": 10000})
	res, err := tool.Execute(input, ctx)
	if err != nil {
		t.Fatal(err)
	}

	content := res.Content.(string)
	if !strings.Contains(content, "truncated") {
		t.Errorf("expected truncated marker")
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], "exit: ") {
		t.Errorf("expected exit line last, got %v", lines[len(lines)-1])
	}
}
