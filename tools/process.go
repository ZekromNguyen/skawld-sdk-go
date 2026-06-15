package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

// ─── Process Manager ───────────────────────────────────────────────────────

// ProcessRecord tracks a single background process.
type ProcessRecord struct {
	ID        string
	Command   string
	Status    string // "running", "done", "error", "killed"
	PID       int
	StartedAt time.Time
	StoppedAt time.Time
	ExitCode  int
	Output    string
	cmd       *exec.Cmd
	mu        sync.Mutex
}

// processRegistry manages background processes across tool calls.
var processRegistry = struct {
	sync.RWMutex
	procs map[string]*ProcessRecord
	seq   int
}{procs: make(map[string]*ProcessRecord)}

// ProcessTool manages background processes: list, poll, log, kill.
type ProcessTool struct{}

func (ProcessTool) Name() string        { return "Process" }
func (ProcessTool) Description() string {
	return "Manage background processes. Actions: list (show all processes), poll (check status of one), logs (get output of one), kill (terminate one)."
}
func (ProcessTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform: list, poll, logs, kill, spawn",
				"enum":        []string{"list", "poll", "logs", "kill", "spawn"},
			},
			"pid": map[string]interface{}{
				"type":        "string",
				"description": "Process ID (required for poll, logs, kill)",
			},
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Command to run in background (required for spawn)",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Brief description of the spawned command",
			},
		},
		"required": []string{"action"},
	}
}
func (ProcessTool) Scope() core.ToolScope       { return core.ToolScopeExec }
func (ProcessTool) ParallelSafe() bool          { return true }

func (t ProcessTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	action, _ := asString(raw["action"])
	if action == "" {
		return nil, core.NewToolExecutionError("Process", "action is required")
	}

	validActions := map[string]bool{"list": true, "poll": true, "logs": true, "kill": true, "spawn": true}
	if !validActions[action] {
		return nil, core.NewToolExecutionError("Process", fmt.Sprintf("unknown action: %s", action))
	}

	pid, _ := asString(raw["pid"])
	cmd, _ := asString(raw["command"])
	desc, _ := asString(raw["description"])

	if (action == "poll" || action == "logs" || action == "kill") && pid == "" {
		return nil, core.NewToolExecutionError("Process", fmt.Sprintf("pid is required for action %q", action))
	}
	if action == "spawn" && cmd == "" {
		return nil, core.NewToolExecutionError("Process", "command is required for spawn")
	}

	return map[string]interface{}{
		"action":      action,
		"pid":         pid,
		"command":     cmd,
		"description": desc,
	}, nil
}

func (t ProcessTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	action, _ := input["action"].(string)
	pid, _ := input["pid"].(string)
	command, _ := input["command"].(string)
	description, _ := input["description"].(string)

	switch action {
	case "list":
		return listProcesses()
	case "poll":
		return pollProcess(pid)
	case "logs":
		return getProcessLogs(pid)
	case "kill":
		return killProcess(pid)
	case "spawn":
		return spawnProcess(ctx.Context, command, description)
	default:
		return core.ToolResult{Content: fmt.Sprintf("Unknown action: %s", action), IsError: true}, nil
	}
}

func (t ProcessTool) Summarize(input map[string]interface{}) string {
	action, _ := input["action"].(string)
	pid, _ := input["pid"].(string)
	switch action {
	case "spawn":
		cmd, _ := input["command"].(string)
		return fmt.Sprintf("Process spawn: %s", truncate(cmd, 50))
	case "list":
		return "Process list"
	case "poll", "logs", "kill":
		if pid != "" {
			return fmt.Sprintf("Process %s: %s", action, truncate(pid, 16))
		}
		return fmt.Sprintf("Process %s", action)
	default:
		return "Process"
	}
}

func listProcesses() (core.ToolResult, error) {
	processRegistry.RLock()
	defer processRegistry.RUnlock()

	if len(processRegistry.procs) == 0 {
		return core.ToolResult{Content: "No background processes.", Summary: "Process list: 0"}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-8s %-8s %-30s %s\n", "ID", "STATUS", "COMMAND", "AGE"))
	sb.WriteString(strings.Repeat("-", 70) + "\n")

	for _, p := range processRegistry.procs {
		p.mu.Lock()
		status := p.Status
		cmd := truncate(p.Command, 30)
		age := time.Since(p.StartedAt).Round(time.Second).String()
		p.mu.Unlock()
		sb.WriteString(fmt.Sprintf("%-8s %-8s %-30s %s\n", p.ID, status, cmd, age))
	}

	count := len(processRegistry.procs)
	return core.ToolResult{
		Content: sb.String(),
		Summary: fmt.Sprintf("Process list: %d running", count),
	}, nil
}

func pollProcess(pid string) (core.ToolResult, error) {
	processRegistry.RLock()
	p, ok := processRegistry.procs[pid]
	processRegistry.RUnlock()

	if !ok {
		return core.ToolResult{Content: fmt.Sprintf("Process %s not found.", pid), IsError: true}, nil
	}

	p.mu.Lock()
	status := p.Status
	exitCode := p.ExitCode
	p.mu.Unlock()

	return core.ToolResult{
		Content: fmt.Sprintf("Process %s: %s (exit=%d)", pid, status, exitCode),
		Summary: fmt.Sprintf("Process %s: %s", pid, status),
	}, nil
}

func getProcessLogs(pid string) (core.ToolResult, error) {
	processRegistry.RLock()
	p, ok := processRegistry.procs[pid]
	processRegistry.RUnlock()

	if !ok {
		return core.ToolResult{Content: fmt.Sprintf("Process %s not found.", pid), IsError: true}, nil
	}

	p.mu.Lock()
	output := p.Output
	if output == "" {
		output = "(no output yet)"
	}
	status := p.Status
	p.mu.Unlock()

	return core.ToolResult{
		Content: fmt.Sprintf("Process %s [%s]:\n%s", pid, status, output),
		Summary: fmt.Sprintf("Process %s logs: %d chars", pid, len(output)),
	}, nil
}

func killProcess(pid string) (core.ToolResult, error) {
	processRegistry.Lock()
	p, ok := processRegistry.procs[pid]
	if !ok {
		processRegistry.Unlock()
		return core.ToolResult{Content: fmt.Sprintf("Process %s not found.", pid), IsError: true}, nil
	}
	delete(processRegistry.procs, pid)
	processRegistry.Unlock()

	p.mu.Lock()
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.Status = "killed"
	p.cmd = nil
	p.mu.Unlock()

	return core.ToolResult{
		Content: fmt.Sprintf("Process %s killed.", pid),
		Summary: fmt.Sprintf("Process %s: killed", pid),
	}, nil
}

func spawnProcess(ctx context.Context, command string, description string) (core.ToolResult, error) {
	processRegistry.Lock()
	processRegistry.seq++
	id := fmt.Sprintf("bg-%d", processRegistry.seq)
	processRegistry.Unlock()

	p := &ProcessRecord{
		ID:        id,
		Command:   command,
		Status:    "running",
		StartedAt: time.Now(),
	}

	p.cmd = exec.CommandContext(context.Background(), "sh", "-c", command)
	p.cmd.Stdout = &processWriter{p: p}
	p.cmd.Stderr = &processWriter{p: p}

	if err := p.cmd.Start(); err != nil {
		p.Status = "error"
		return core.ToolResult{
			Content: fmt.Sprintf("Failed to start process: %v", err),
			IsError: true,
		}, nil
	}

	p.PID = p.cmd.Process.Pid

	processRegistry.Lock()
	processRegistry.procs[id] = p
	processRegistry.Unlock()

	// Wait for completion in background goroutine
	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				p.ExitCode = exitErr.ExitCode()
				p.Status = "done"
			} else {
				p.Status = "error"
				p.ExitCode = -1
			}
		} else {
			p.Status = "done"
			p.ExitCode = 0
		}
		p.StoppedAt = time.Now()
		p.cmd = nil
		p.mu.Unlock()
	}()

	return core.ToolResult{
		Content: fmt.Sprintf("Started background process:\n  ID: %s\n  PID: %d\n  Command: %s\nUse 'poll' to check status, 'logs' for output, 'kill' to terminate.",
			id, p.PID, command),
		Summary: fmt.Sprintf("Process spawned: %s (%s)", id, truncate(description, 40)),
	}, nil
}

// processWriter accumulates output from a background process.
type processWriter struct {
	p *ProcessRecord
}

func (w *processWriter) Write(data []byte) (int, error) {
	w.p.mu.Lock()
	w.p.Output += string(data)
	if len(w.p.Output) > 65536 {
		w.p.Output = w.p.Output[len(w.p.Output)-65536:]
	}
	w.p.mu.Unlock()
	return len(data), nil
}