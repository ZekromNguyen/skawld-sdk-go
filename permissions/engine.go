package permissions

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
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
	ToolName   string
	ToolUseID  string
	Input      map[string]interface{}
	Summary    string
	Mode       core.PermissionMode
	Principal  core.Principal
	Descriptor core.ToolDescriptor
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
	Tools    []string
	Paths    []string
	Pattern  interface{}
	Decision DecisionKind
}

type Options struct {
	Mode        core.PermissionMode
	Rules       []Rule
	CanUseTool  CanUseTool
	ProjectRoot string
	Observer    core.Observer
}

type PendingCall struct {
	ToolUseID string
	Tool      core.Tool
	Input     map[string]interface{}
	CWD       string
	SessionID string
	RunID     string
	Principal core.Principal
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
	if reason := validatePendingCall(call); reason != "" {
		return Decision{Decision: DecisionDeny, Reason: reason}
	}
	if decision, ok := e.evaluateRules(call); ok {
		return decision
	}
	return modeDefault(call.Tool, e.opts.Mode)
}

func (e *Engine) evaluateRules(call PendingCall) (Decision, bool) {
	for index, rule := range e.opts.Rules {
		if rule.Decision != DecisionAllow && rule.Decision != DecisionDeny {
			continue
		}
		if rule.Kind == "tool" && matchToolRule(rule, call) {
			return fromRuleDecision(rule.Decision, fmt.Sprintf("%s rule matched %s.", rule.Kind, call.Tool.Name())), true
		}
		if rule.Kind == "path" && matchPathRule(rule, call, e.opts.ProjectRoot) {
			return fromRuleDecision(rule.Decision, fmt.Sprintf("%s rule matched %s.", rule.Kind, call.Tool.Name())), true
		}
		if rule.Kind == "bash" && call.Tool.Name() == "Bash" {
			command, ok := call.Input["command"].(string)
			if !ok {
				continue
			}
			if bashDecision, ok := evaluateBashRules(bashRulesFrom(e.opts.Rules, index), command); ok {
				return fromRuleDecision(bashDecision, fmt.Sprintf("%s rule matched %s.", rule.Kind, call.Tool.Name())), true
			}
		}
	}
	return Decision{}, false
}

func (e *Engine) Resolve(ctx context.Context, call PendingCall) Decision {
	initial := e.Evaluate(call)
	if initial.Decision != DecisionAsk {
		return initial
	}
	if e.opts.CanUseTool == nil {
		return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: canUseTool callback is not configured.", call.Tool.Name())}
	}
	if ctx.Err() != nil {
		return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: permission request aborted.", call.Tool.Name())}
	}
	resp, err := e.callPermissionCallback(ctx, call)
	if err != nil {
		return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: permission callback failed or aborted.", call.Tool.Name())}
	}
	if resp.Behavior == "deny" {
		if resp.Message == "" {
			return invalidCallbackResponseDecision(call.Tool.Name())
		}
		return Decision{Decision: DecisionDeny, Reason: resp.Message}
	}
	if resp.Behavior != "allow" {
		return invalidCallbackResponseDecision(call.Tool.Name())
	}
	if resp.UpdatedInput != nil {
		validated, err := call.Tool.Validate(resp.UpdatedInput)
		if err != nil {
			return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: updated input is invalid.", call.Tool.Name())}
		}
		if validated == nil {
			return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: updated input is invalid.", call.Tool.Name())}
		}
		return Decision{Decision: DecisionAllow, UpdatedInput: validated}
	}
	return Decision{Decision: DecisionAllow}
}

func (e *Engine) callPermissionCallback(ctx context.Context, call PendingCall) (CanUseToolResponse, error) {
	start := time.Now()
	resp, err := e.opts.CanUseTool(ctx, CanUseToolRequest{
		ToolName:   call.Tool.Name(),
		ToolUseID:  call.ToolUseID,
		Input:      call.Input,
		Summary:    call.Tool.Summarize(call.Input),
		Mode:       e.opts.Mode,
		Principal:  call.Principal,
		Descriptor: core.DescribeTool(call.Tool),
	})
	if e.opts.Observer != nil {
		e.opts.Observer.Observe(ctx, core.Observation{
			Type:       core.ObservationPermissionCallback,
			Operation:  "can_use_tool",
			SessionID:  call.SessionID,
			RunID:      call.RunID,
			TenantID:   call.Principal.TenantID,
			ActorID:    call.Principal.ActorID,
			ToolName:   call.Tool.Name(),
			DurationMS: time.Since(start).Milliseconds(),
			Error:      err,
		})
	}
	if err != nil {
		return CanUseToolResponse{}, fmt.Errorf("permission callback for %s after %s: %w", call.Tool.Name(), time.Since(start), err)
	}
	return resp, nil
}

func modeDefault(tool core.Tool, mode core.PermissionMode) Decision {
	if mode == core.PermissionModeYolo {
		return Decision{Decision: DecisionAllow}
	}
	descriptor := core.DescribeTool(tool)
	if descriptor.Risk == core.RiskCritical || descriptor.Risk == core.RiskHigh || descriptor.NetworkAccess {
		return Decision{Decision: DecisionAsk}
	}
	if tool.Scope() == core.ToolScopeRead || isTaskTool(tool.Name()) {
		return Decision{Decision: DecisionAllow}
	}
	if tool.Scope() == core.ToolScopeWrite && mode == core.PermissionModeAcceptEdits && descriptor.SideEffect != core.SideEffectNonIdempotent {
		return Decision{Decision: DecisionAllow}
	}
	return Decision{Decision: DecisionAsk}
}

func isTaskTool(name string) bool {
	switch name {
	case "TaskCreate", "TaskList", "TaskGet", "TaskUpdate":
		return true
	default:
		return false
	}
}

func fromRuleDecision(decision DecisionKind, reason string) Decision {
	if decision == DecisionAllow {
		return Decision{Decision: DecisionAllow}
	}
	return Decision{Decision: DecisionDeny, Reason: reason}
}

func validatePendingCall(call PendingCall) string {
	if strings.TrimSpace(call.ToolUseID) == "" {
		return "Invalid permission call: tool_use_id must be a non-empty string."
	}
	if call.Input == nil {
		return "Invalid permission call: input must be an object."
	}
	if call.Tool == nil {
		return "Invalid permission call: tool is invalid."
	}
	return ""
}

func invalidCallbackResponseDecision(toolName string) Decision {
	return Decision{Decision: DecisionDeny, Reason: fmt.Sprintf("Permission denied for %s: permission callback returned an invalid response.", toolName)}
}

func matchToolRule(rule Rule, call PendingCall) bool {
	callName := call.Tool.Name()
	if rule.Tool != "*" && rule.Tool != callName {
		return false
	}
	if rule.Arg == "" {
		return true
	}
	if callName != "Skill" {
		return false
	}
	if rule.Arg == "*" {
		return true
	}
	skillName, ok := call.Input["skill"].(string)
	return ok && skillName == rule.Arg
}

func matchPathRule(rule Rule, call PendingCall, projectRoot string) bool {
	tools := rule.Tools
	if len(tools) == 0 {
		tools = []string{"Write", "Edit"}
	}
	if !containsString(tools, call.Tool.Name()) {
		return false
	}
	cwd := call.CWD
	if cwd == "" {
		cwd = projectRoot
	}
	rawPath, ok := pathInputForTool(call.Tool.Name(), call.Input, cwd)
	if !ok {
		return false
	}
	target := normalizeForGlob(resolvePath(cwd, rawPath))
	patternRoot := normalizeForGlob(resolvePath("", projectRoot))
	for _, pattern := range pathPatterns(rule) {
		if matchesPathPattern(pattern, target, patternRoot) {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func pathInputForTool(tool string, input map[string]interface{}, cwd string) (string, bool) {
	switch tool {
	case "Read", "Write", "Edit":
		value, ok := input["file_path"].(string)
		return value, ok && strings.TrimSpace(value) != ""
	case "Glob", "Grep":
		value, ok := input["path"].(string)
		if ok && strings.TrimSpace(value) != "" {
			return value, true
		}
		return cwd, true
	default:
		return "", false
	}
}

func pathPatterns(rule Rule) []string {
	if len(rule.Paths) > 0 {
		return rule.Paths
	}
	if rule.Path != "" {
		return []string{rule.Path}
	}
	return nil
}

func matchesPathPattern(patternValue, target, projectRoot string) bool {
	inverted := strings.HasPrefix(patternValue, "!")
	body := patternValue
	if inverted {
		body = strings.TrimPrefix(patternValue, "!")
	}
	if body == "" {
		return false
	}
	absolutePattern := body
	if !isAbsPath(body) {
		absolutePattern = resolvePath(projectRoot, body)
	}
	matched := globMatch(normalizeForGlob(absolutePattern), target)
	if inverted {
		return !matched
	}
	return matched
}

func resolvePath(base, value string) string {
	if value == "" {
		value = "."
	}
	value = normalizeForGlob(value)
	if isAbsPath(value) || base == "" {
		return path.Clean(value)
	}
	return path.Clean(path.Join(normalizeForGlob(base), value))
}

func isAbsPath(value string) bool {
	value = normalizeForGlob(value)
	if strings.HasPrefix(value, "/") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && value[2] == '/'
}

func normalizeForGlob(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

func globMatch(patternValue, target string) bool {
	if strings.HasSuffix(patternValue, "/**") {
		root := strings.TrimSuffix(patternValue, "/**")
		if target == root || strings.HasPrefix(target, root+"/") {
			return true
		}
	}
	re, err := regexp.Compile(globRegex(patternValue))
	if err != nil {
		return false
	}
	return re.MatchString(target)
}

func globRegex(patternValue string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(patternValue); i++ {
		ch := patternValue[i]
		switch ch {
		case '*':
			if i+1 < len(patternValue) && patternValue[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := i + 1
			for end < len(patternValue) && patternValue[end] != ']' {
				end++
			}
			if end < len(patternValue) {
				b.WriteString(patternValue[i : end+1])
				i = end
			} else {
				b.WriteString(regexp.QuoteMeta(string(ch)))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return b.String()
}

func bashRulesFrom(rules []Rule, startIndex int) []Rule {
	var out []Rule
	for _, rule := range rules[startIndex:] {
		if rule.Kind == "bash" && (rule.Decision == DecisionAllow || rule.Decision == DecisionDeny) {
			out = append(out, rule)
		}
	}
	return out
}

func evaluateBashRules(rules []Rule, command string) (DecisionKind, bool) {
	segments := splitCompositeCommand(command)
	if len(segments) == 0 {
		return "", false
	}
	allAllowed := true
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		decision, ok := firstMatchingBashDecision(rules, segment)
		if ok && decision == DecisionDeny {
			return DecisionDeny, true
		}
		if !ok || decision != DecisionAllow {
			allAllowed = false
		}
	}
	if allAllowed {
		return DecisionAllow, true
	}
	return "", false
}

func firstMatchingBashDecision(rules []Rule, commandSegment string) (DecisionKind, bool) {
	for _, rule := range rules {
		if matchBashRule(rule, commandSegment) {
			return rule.Decision, true
		}
	}
	return "", false
}

func matchBashRule(rule Rule, commandSegment string) bool {
	segment := strings.TrimSpace(commandSegment)
	if regex, ok := bashRuleRegex(rule); ok {
		re, err := regexp.Compile(regex)
		return err == nil && re.MatchString(segment)
	}
	patternValue := bashRulePlainPattern(rule)
	if patternValue == "" {
		return false
	}
	if strings.ContainsAny(patternValue, "*?[") {
		return legacyWildcardMatch(patternValue, segment)
	}
	return tokensStartWith(tokenizeShellPrefix(segment), tokenizeShellPrefix(patternValue))
}

func bashRuleRegex(rule Rule) (string, bool) {
	switch patternValue := rule.Pattern.(type) {
	case map[string]string:
		regex := patternValue["regex"]
		return regex, regex != ""
	case map[string]interface{}:
		regex, ok := patternValue["regex"].(string)
		return regex, ok && regex != ""
	case struct{ Regex string }:
		return patternValue.Regex, patternValue.Regex != ""
	}
	return "", false
}

func bashRulePlainPattern(rule Rule) string {
	if patternValue, ok := rule.Pattern.(string); ok {
		return patternValue
	}
	if rule.Command != "" {
		return rule.Command
	}
	return rule.Arg
}

func tokenizeShellPrefix(input string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	inToken := false
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			} else {
				current.WriteRune(ch)
			}
			continue
		}
		if quote == '"' {
			if ch == '"' {
				quote = 0
			} else if ch == '\\' && i+1 < len(runes) {
				i++
				current.WriteRune(runes[i])
			} else {
				current.WriteRune(ch)
			}
			continue
		}
		if isShellSpace(ch) {
			if inToken {
				tokens = append(tokens, current.String())
				current.Reset()
				inToken = false
			}
			continue
		}
		inToken = true
		if ch == '\'' || ch == '"' {
			quote = ch
		} else if ch == '\\' && i+1 < len(runes) {
			i++
			current.WriteRune(runes[i])
		} else {
			current.WriteRune(ch)
		}
	}
	if inToken {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func isShellSpace(ch rune) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func tokensStartWith(tokens, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(tokens) {
		return false
	}
	for i, token := range prefix {
		if tokens[i] != token {
			return false
		}
	}
	return true
}

func splitCompositeCommand(input string) []string {
	var segments []string
	start := 0
	var quote rune
	escaped := false
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if escaped {
			escaped = false
			continue
		}
		if quote != '\'' && ch == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		operatorLength := compositeOperatorLength(runes, i)
		if operatorLength > 0 {
			segments = append(segments, string(runes[start:i]))
			i += operatorLength - 1
			start = i + 1
		}
	}
	segments = append(segments, string(runes[start:]))
	var filtered []string
	for _, segment := range segments {
		if strings.TrimSpace(segment) != "" {
			filtered = append(filtered, segment)
		}
	}
	return filtered
}

func compositeOperatorLength(input []rune, index int) int {
	ch := input[index]
	var next rune
	if index+1 < len(input) {
		next = input[index+1]
	}
	if (ch == '&' && next == '&') || (ch == '|' && next == '|') {
		return 2
	}
	if ch == ';' || ch == '|' {
		return 1
	}
	return 0
}

func legacyWildcardMatch(patternValue, value string) bool {
	regex := globRegex(normalizeForGlob(patternValue))
	re, err := regexp.Compile(regex)
	if err != nil {
		return false
	}
	return re.MatchString(normalizeForGlob(value))
}
