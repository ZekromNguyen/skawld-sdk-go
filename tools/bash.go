package tools

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

const streamCap = 30000
const combinedCap = 30000
const bashCleanupGrace = 2 * time.Second

type accumulator struct {
	text           []byte
	truncated      bool
	truncatedBytes int
}

func (a *accumulator) Write(p []byte) (n int, err error) {
	if a.truncated {
		a.truncatedBytes += len(p)
		return len(p), nil
	}
	headroom := streamCap - len(a.text)
	if len(p) <= headroom {
		a.text = append(a.text, p...)
	} else {
		a.text = append(a.text, p[:headroom]...)
		a.truncatedBytes += len(p) - headroom
		a.truncated = true
	}
	return len(p), nil
}

type BashTool struct{}

func (BashTool) Name() string { return "Bash" }
func (BashTool) Description() string {
	return "Run a shell command. Returns stdout, stderr, and the exit code. Non-zero exit codes are not errors — the model interprets them."
}
func (BashTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command":     map[string]interface{}{"type": "string", "description": "Shell command to execute."},
			"timeout_ms":  map[string]interface{}{"type": "number", "description": "Timeout in milliseconds. Defaults to 120000, max 1800000."},
			"description": map[string]interface{}{"type": "string", "description": "Brief description of what the command does."},
		},
		"required": []string{"command"},
	}
}
func (BashTool) Scope() core.ToolScope { return core.ToolScopeExec }
func (BashTool) ParallelSafe() bool    { return false }

func (t BashTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseBashInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}

func (t BashTool) Summarize(input map[string]interface{}) string {
	if desc, ok := asString(input["description"]); ok && desc != "" {
		return desc
	}
	cmd := input["command"].(string)
	if len(cmd) > 60 {
		cmd = cmd[:60] + "…"
	}
	return "Bash: " + cmd
}

func formatOutput(stdout, stderr *accumulator, exitCode int) string {
	combined := string(stdout.text)
	if len(stderr.text) > 0 {
		combined += "\n---\n" + string(stderr.text)
	}

	truncationNote := ""
	totalTruncated := 0
	if stdout.truncated {
		totalTruncated += stdout.truncatedBytes
	}
	if stderr.truncated {
		totalTruncated += stderr.truncatedBytes
	}

	if totalTruncated > 0 {
		truncationNote = fmt.Sprintf("\n… (%d chars truncated)", totalTruncated)
	}

	if len(combined) > combinedCap {
		omitted := len(combined) - combinedCap
		combined = combined[:combinedCap]
		truncationNote = fmt.Sprintf("\n… (%d chars truncated)", omitted+totalTruncated)
	}

	return fmt.Sprintf("%s%s\nexit: %d", combined, truncationNote, exitCode)
}

func (t BashTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	if ctx.Context.Err() != nil {
		return core.ToolResult{
			Content: "Bash: aborted by signal before execution.",
			Summary: t.Summarize(input),
			IsError: true,
		}, nil
	}

	timeoutMs := asInt(input["timeout_ms"], 120000)
	shell := "/bin/sh"
	shellFlag := "-c"
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
		shellFlag = "/c"
	}

	cmd := exec.Command(shell, shellFlag, input["command"].(string))
	cmd.Dir = ctx.CWD
	setupProcessOptions(cmd)

	stdout := &accumulator{}
	stderr := &accumulator{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Start()
	if err != nil {
		return core.ToolResult{
			Content: fmt.Sprintf("Bash: failed to spawn shell: %v", err),
			Summary: t.Summarize(input),
			IsError: true,
		}, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case err := <-done:
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				// E.g. ENOENT error internally? Start usually catches that.
				exitCode = 1
			}
		}
		return core.ToolResult{
			Content: formatOutput(stdout, stderr, exitCode),
			Summary: t.Summarize(input),
		}, nil
	case <-timer.C:
		_ = terminateProcessTree(cmd, done, bashCleanupGrace)
		return core.ToolResult{
			Content: fmt.Sprintf("Bash: timed out after %d ms.", timeoutMs),
			Summary: t.Summarize(input),
			IsError: true,
		}, nil
	case <-ctx.Context.Done():
		_ = terminateProcessTree(cmd, done, bashCleanupGrace)
		return core.ToolResult{
			Content: "Bash: aborted by signal.",
			Summary: t.Summarize(input),
			IsError: true,
		}, nil
	}
}
