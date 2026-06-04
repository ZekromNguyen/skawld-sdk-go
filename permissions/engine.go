package permissions

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type DecisionKind string

const (
	DecisionAllow DecisionKind = "allow"
	DecisionDeny  DecisionKind = "deny"
	DecisionAsk   DecisionKind = "ask"
)

type Decision struct {
	Decision     DecisionKind
	Reason       string
	UpdatedInput map[string]interface{}
}

type CanUseToolRequest struct {
	ToolName  string
	ToolUseID string
	Input     map[string]interface{}
	Summary   string
	Mode      core.PermissionMode
}

type CanUseToolResponse struct {
	Behavior     string
	Message      string
	UpdatedInput map[string]interface{}
}

type CanUseTool func(context.Context, CanUseToolRequest) (CanUseToolResponse, error)

type Rule struct {
	Kind     string
	Tool     string
	Arg      string
	Value    string
	Command  string
	Path     string
	Decision DecisionKind
}

type Options struct {
	Mode        core.PermissionMode
	Rules       []Rule
	CanUseTool  CanUseTool
	ProjectRoot string
}

type PendingCall struct {
	ToolUseID string
	Tool      core.Tool
	Input     map[string]interface{}
	CWD       string
}

type Engine struct {
	opts Options
}

func NewEngine(opts Options) *Engine {
	if opts.Mode == "" {
		opts.Mode = core.PermissionModeDefault
	}
	return &Engine{opts: opts}
}

func (e *Engine) Evaluate(call PendingCall) Decision {
	if call.ToolUseID == "" {
		return Decision{Decision: DecisionDeny, Reason: "tool_use_id is required"}
	}
	if call.Tool == nil {
		return Decision{Decision: DecisionDeny, Reason: "tool is invalid"}
	}
	for _, rule := range e.opts.Rules {
		if rule.Decision == "" {
			continue
		}
		if rule.Kind == "tool" && rule.Tool == call.Tool.Name() {
			if inputRuleMatches(rule, call.Input) {
				return Decision{Decision: rule.Decision, Reason: fmt.Sprintf("tool rule matched %s", call.Tool.Name())}
			}
		}
		if rule.Kind == "bash" && bashRuleMatches(rule, call) {
			return Decision{Decision: rule.Decision, Reason: fmt.Sprintf("bash rule matched %s", call.Tool.Name())}
		}
		if rule.Kind == "path" && pathRuleMatches(rule, call, e.opts.ProjectRoot) {
			return Decision{Decision: rule.Decision, Reason: fmt.Sprintf("path rule matched %s", call.Tool.Name())}
		}
	}
	return modeDefault(call.Tool, e.opts.Mode)
}

func (e *Engine) Resolve(ctx context.Context, call PendingCall) Decision {
	initial := e.Evaluate(call)
	if initial.Decision != DecisionAsk {
		return initial
	}
	if e.opts.CanUseTool == nil {
		return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: canUseTool callback is not configured.", call.Tool.Name())}
	}
	resp, err := e.opts.CanUseTool(ctx, CanUseToolRequest{
		ToolName: call.Tool.Name(), ToolUseID: call.ToolUseID,
		Input: call.Input, Summary: call.Tool.Summarize(call.Input), Mode: e.opts.Mode,
	})
	if err != nil {
		return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: permission callback failed.", call.Tool.Name())}
	}
	if resp.Behavior == "deny" {
		return Decision{Decision: DecisionDeny, Reason: resp.Message}
	}
	if resp.Behavior != "allow" {
		return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: permission callback returned an invalid response.", call.Tool.Name())}
	}
	if resp.UpdatedInput != nil {
		validated, err := call.Tool.Validate(resp.UpdatedInput)
		if err != nil {
			return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: updated input is invalid.", call.Tool.Name())}
		}
		return Decision{Decision: DecisionAllow, UpdatedInput: validated}
	}
	return Decision{Decision: DecisionAllow}
}

func modeDefault(tool core.Tool, mode core.PermissionMode) Decision {
	if tool.Scope() == core.ToolScopeRead || strings.HasPrefix(tool.Name(), "Task") {
		return Decision{Decision: DecisionAllow}
	}
	if mode == core.PermissionModeYolo {
		return Decision{Decision: DecisionAllow}
	}
	if tool.Scope() == core.ToolScopeWrite && mode == core.PermissionModeAcceptEdits {
		return Decision{Decision: DecisionAllow}
	}
	return Decision{Decision: DecisionAsk}
}

func inputRuleMatches(rule Rule, input map[string]interface{}) bool {
	if rule.Arg == "" {
		return true
	}
	v, exists := input[rule.Arg]
	if !exists {
		if rule.Value == "" {
			return matchesAnyInput(rule.Arg, input)
		}
		return false
	}
	if rule.Value == "" {
		return true
	}
	if s, ok := v.(string); ok {
		if wildcardMatch(rule.Value, s) {
			return true
		}
	}
	if fmt.Sprint(v) == rule.Value {
		return true
	}
	return false
}

func bashRuleMatches(rule Rule, call PendingCall) bool {
	if call.Tool.Name() != "Bash" {
		return false
	}
	command, ok := call.Input["command"].(string)
	if !ok || command == "" {
		return false
	}
	pattern := rule.Command
	if pattern == "" {
		pattern = rule.Arg
	}
	if pattern == "" {
		return true
	}
	return wildcardMatch(pattern, command)
}

func wildcardMatch(pattern, value string) bool {
	if pattern == value {
		return true
	}
	if ok, _ := filepath.Match(pattern, value); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, filepath.ToSlash(value)); ok {
		return true
	}
	return false
}

func matchesAnyInput(value string, input map[string]interface{}) bool {
	for _, v := range input {
		if s, ok := v.(string); ok && s == value {
			return true
		}
	}
	return false
}

func pathRuleMatches(rule Rule, call PendingCall, projectRoot string) bool {
	target := ""
	if s, ok := call.Input["file_path"].(string); ok {
		target = s
	} else if s, ok := call.Input["path"].(string); ok {
		target = s
	}
	if target == "" {
		return false
	}
	if !filepath.IsAbs(target) {
		root := call.CWD
		if root == "" {
			root = projectRoot
		}
		target = filepath.Join(root, target)
	}
	pat := rule.Path
	if pat == "" {
		return false
	}
	if ok, _ := filepath.Match(pat, target); ok {
		return true
	}
	return strings.Contains(filepath.ToSlash(target), filepath.ToSlash(pat))
}
