package permissions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

const testProjectRoot = "/repo"

type testTool struct {
	name     string
	scope    core.ToolScope
	validate func(map[string]interface{}) (map[string]interface{}, error)
}

func (t testTool) Name() string        { return t.name }
func (t testTool) Description() string { return t.name + " description" }
func (t testTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t testTool) Scope() core.ToolScope { return t.scope }
func (t testTool) ParallelSafe() bool    { return true }
func (t testTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	if t.validate != nil {
		return t.validate(raw)
	}
	return raw, nil
}
func (t testTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{Content: "ok", Summary: "ok"}, nil
}
func (t testTool) Summarize(input map[string]interface{}) string { return t.name }

func newTestEngine(opts Options) *Engine {
	if opts.ProjectRoot == "" {
		opts.ProjectRoot = testProjectRoot
	}
	return NewEngine(opts)
}

func call(id string, tool core.Tool, input map[string]interface{}) PendingCall {
	if input == nil {
		input = map[string]interface{}{}
	}
	return PendingCall{ToolUseID: id, Tool: tool, Input: input}
}

func TestPermissionModeDefaults(t *testing.T) {
	readTool := testTool{name: "Read", scope: core.ToolScopeRead}
	writeTool := testTool{name: "Write", scope: core.ToolScopeWrite}
	editTool := testTool{name: "Edit", scope: core.ToolScopeWrite}
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}
	taskCreateTool := testTool{name: "TaskCreate", scope: core.ToolScopeWrite}

	for _, mode := range []core.PermissionMode{core.PermissionModeDefault, core.PermissionModeAcceptEdits, core.PermissionModeYolo} {
		if got := newTestEngine(Options{Mode: mode}).Evaluate(call("toolu_1", readTool, map[string]interface{}{"file_path": "README.md"})).Decision; got != DecisionAllow {
			t.Fatalf("read tool in %s expected allow, got %s", mode, got)
		}
	}
	if got := newTestEngine(Options{Mode: core.PermissionModeDefault}).Evaluate(call("toolu_1", writeTool, nil)).Decision; got != DecisionAsk {
		t.Fatalf("Write in default expected ask, got %s", got)
	}
	if got := newTestEngine(Options{Mode: core.PermissionModeDefault}).Evaluate(call("toolu_1", editTool, nil)).Decision; got != DecisionAsk {
		t.Fatalf("Edit in default expected ask, got %s", got)
	}
	for _, mode := range []core.PermissionMode{core.PermissionModeAcceptEdits, core.PermissionModeYolo} {
		if got := newTestEngine(Options{Mode: mode}).Evaluate(call("toolu_1", writeTool, nil)).Decision; got != DecisionAllow {
			t.Fatalf("Write in %s expected allow, got %s", mode, got)
		}
		if got := newTestEngine(Options{Mode: mode}).Evaluate(call("toolu_1", editTool, nil)).Decision; got != DecisionAllow {
			t.Fatalf("Edit in %s expected allow, got %s", mode, got)
		}
	}
	if got := newTestEngine(Options{Mode: core.PermissionModeDefault}).Evaluate(call("toolu_1", bashTool, map[string]interface{}{"command": "git status"})).Decision; got != DecisionAsk {
		t.Fatalf("Bash in default expected ask, got %s", got)
	}
	if got := newTestEngine(Options{Mode: core.PermissionModeAcceptEdits}).Evaluate(call("toolu_1", bashTool, map[string]interface{}{"command": "git status"})).Decision; got != DecisionAsk {
		t.Fatalf("Bash in acceptEdits expected ask, got %s", got)
	}
	if got := newTestEngine(Options{Mode: core.PermissionModeYolo}).Evaluate(call("toolu_1", bashTool, map[string]interface{}{"command": "git status"})).Decision; got != DecisionAllow {
		t.Fatalf("Bash in yolo expected allow, got %s", got)
	}
	if got := newTestEngine(Options{Mode: core.PermissionModeDefault}).Evaluate(call("toolu_1", taskCreateTool, nil)).Decision; got != DecisionAllow {
		t.Fatalf("TaskCreate expected allow, got %s", got)
	}
	decision := newTestEngine(Options{
		Mode:  core.PermissionModeDefault,
		Rules: []Rule{{Kind: "tool", Tool: "TaskCreate", Decision: DecisionDeny}},
	}).Evaluate(call("toolu_1", taskCreateTool, nil))
	if decision.Decision != DecisionDeny {
		t.Fatalf("explicit task deny expected deny, got %s", decision.Decision)
	}
}

func TestPermissionRulePrecedence(t *testing.T) {
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}

	tests := []struct {
		name     string
		mode     core.PermissionMode
		rules    []Rule
		command  string
		expected DecisionKind
	}{
		{
			name: "allow before deny",
			mode: core.PermissionModeDefault,
			rules: []Rule{
				{Kind: "tool", Tool: "Bash", Decision: DecisionAllow},
				{Kind: "tool", Tool: "Bash", Decision: DecisionDeny},
			},
			command:  "rm -rf dist",
			expected: DecisionAllow,
		},
		{
			name: "deny before allow",
			mode: core.PermissionModeYolo,
			rules: []Rule{
				{Kind: "tool", Tool: "Bash", Decision: DecisionDeny},
				{Kind: "tool", Tool: "Bash", Decision: DecisionAllow},
			},
			command:  "git status",
			expected: DecisionDeny,
		},
		{
			name:     "explicit deny blocks yolo",
			mode:     core.PermissionModeYolo,
			rules:    []Rule{{Kind: "bash", Pattern: "git push", Decision: DecisionDeny}},
			command:  "git push origin main",
			expected: DecisionDeny,
		},
		{
			name:     "explicit allow overrides ask",
			mode:     core.PermissionModeDefault,
			rules:    []Rule{{Kind: "bash", Pattern: "git status", Decision: DecisionAllow}},
			command:  "git status --short",
			expected: DecisionAllow,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newTestEngine(Options{Mode: tt.mode, Rules: tt.rules}).Evaluate(call("toolu_1", bashTool, map[string]interface{}{"command": tt.command})).Decision
			if got != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestToolRuleMatching(t *testing.T) {
	if !matchToolRule(Rule{Kind: "tool", Tool: "Write", Decision: DecisionAllow}, call("toolu_1", testTool{name: "Write"}, nil)) {
		t.Fatal("expected exact tool rule match")
	}
	if matchToolRule(Rule{Kind: "tool", Tool: "Read", Decision: DecisionAllow}, call("toolu_1", testTool{name: "Write"}, nil)) {
		t.Fatal("unexpected nonmatching tool rule match")
	}
	if !matchToolRule(Rule{Kind: "tool", Tool: "*", Decision: DecisionDeny}, call("toolu_1", testTool{name: "Bash"}, nil)) {
		t.Fatal("expected wildcard tool rule match")
	}
	if !matchToolRule(Rule{Kind: "tool", Tool: "Skill", Arg: "commit", Decision: DecisionAllow}, call("toolu_1", testTool{name: "Skill"}, map[string]interface{}{"skill": "commit"})) {
		t.Fatal("expected Skill arg rule to match skill name")
	}
	if matchToolRule(Rule{Kind: "tool", Tool: "Skill", Arg: "commit", Decision: DecisionAllow}, call("toolu_1", testTool{name: "Skill"}, map[string]interface{}{"skill": "deploy"})) {
		t.Fatal("unexpected Skill arg rule match")
	}
	if !matchToolRule(Rule{Kind: "tool", Tool: "Skill", Arg: "*", Decision: DecisionAllow}, call("toolu_1", testTool{name: "Skill"}, map[string]interface{}{"skill": "anything"})) {
		t.Fatal("expected Skill wildcard arg rule match")
	}
	if matchToolRule(Rule{Kind: "tool", Tool: "Write", Arg: "foo", Decision: DecisionAllow}, call("toolu_1", testTool{name: "Write"}, map[string]interface{}{"file_path": "src/a.ts"})) {
		t.Fatal("non-Skill arg rule should not match")
	}
}

func TestPathRuleMatching(t *testing.T) {
	writeTool := testTool{name: "Write", scope: core.ToolScopeWrite}
	editTool := testTool{name: "Edit", scope: core.ToolScopeWrite}
	globTool := testTool{name: "Glob", scope: core.ToolScopeRead}
	grepTool := testTool{name: "Grep", scope: core.ToolScopeRead}

	if !matchPathRule(Rule{Kind: "path", Paths: []string{"/repo/src/**"}, Decision: DecisionAllow}, call("toolu_1", writeTool, map[string]interface{}{"file_path": "src/index.ts"}), testProjectRoot) {
		t.Fatal("expected relative file path to resolve under project root")
	}
	inverted := Rule{Kind: "path", Paths: []string{"!/repo/**"}, Decision: DecisionDeny}
	if !matchPathRule(inverted, call("toolu_1", writeTool, map[string]interface{}{"file_path": "/tmp/outside.ts"}), testProjectRoot) {
		t.Fatal("expected inverted path rule to match outside project")
	}
	if matchPathRule(inverted, call("toolu_1", writeTool, map[string]interface{}{"file_path": "/repo/src/index.ts"}), testProjectRoot) {
		t.Fatal("inverted path rule should not match inside project")
	}
	filtered := Rule{Kind: "path", Tools: []string{"Edit"}, Paths: []string{"/repo/src/**"}, Decision: DecisionAllow}
	if matchPathRule(filtered, call("toolu_1", writeTool, map[string]interface{}{"file_path": "src/index.ts"}), testProjectRoot) {
		t.Fatal("tool-filtered path rule should not match Write")
	}
	if !matchPathRule(filtered, call("toolu_1", editTool, map[string]interface{}{"file_path": "src/index.ts"}), testProjectRoot) {
		t.Fatal("tool-filtered path rule should match Edit")
	}
	cwdCall := call("toolu_1", writeTool, map[string]interface{}{"file_path": "nested.ts"})
	cwdCall.CWD = "/repo/sub"
	if !matchPathRule(Rule{Kind: "path", Paths: []string{"/repo/sub/**"}, Decision: DecisionAllow}, cwdCall, testProjectRoot) {
		t.Fatal("expected relative file path to resolve against call CWD")
	}
	cwdCall.CWD = "/repo/other"
	if matchPathRule(Rule{Kind: "path", Paths: []string{"/repo/sub/**"}, Decision: DecisionAllow}, cwdCall, testProjectRoot) {
		t.Fatal("unexpected CWD path rule match")
	}
	if !matchPathRule(Rule{Kind: "path", Tools: []string{"Glob"}, Paths: []string{"/repo/**"}, Decision: DecisionAllow}, call("toolu_1", globTool, map[string]interface{}{"pattern": "*.ts"}), testProjectRoot) {
		t.Fatal("expected Glob path fallback to project root")
	}
	if !matchPathRule(Rule{Kind: "path", Tools: []string{"Grep"}, Paths: []string{"/repo/**"}, Decision: DecisionAllow}, call("toolu_1", grepTool, map[string]interface{}{"pattern": "PermissionEngine"}), testProjectRoot) {
		t.Fatal("expected Grep path fallback to project root")
	}
	if !matchPathRule(Rule{Kind: "path", Path: "/repo/src/**", Decision: DecisionAllow}, call("toolu_1", writeTool, map[string]interface{}{"file_path": "src/index.ts"}), testProjectRoot) {
		t.Fatal("expected legacy Path field to remain supported")
	}
}

func TestBashRuleMatching(t *testing.T) {
	if !matchBashRule(Rule{Kind: "bash", Pattern: "git status", Decision: DecisionAllow}, `git "status" --short`) {
		t.Fatal("expected plain bash pattern to match quoted token prefix")
	}
	if !matchBashRule(Rule{Kind: "bash", Pattern: `npm run "test unit"`, Decision: DecisionAllow}, `npm run "test unit" -- --watch`) {
		t.Fatal("expected quoted multi-word token prefix to match")
	}
	if !matchBashRule(Rule{Kind: "bash", Pattern: map[string]string{"regex": `^\s*rm\s+-rf\b`}, Decision: DecisionDeny}, " rm -rf dist") {
		t.Fatal("expected regex bash pattern to match")
	}
	if matchBashRule(Rule{Kind: "bash", Pattern: map[string]string{"regex": "["}, Decision: DecisionDeny}, "rm -rf dist") {
		t.Fatal("invalid regex should not match")
	}
	if matchBashRule(Rule{Kind: "bash", Pattern: "git status", Decision: DecisionAllow}, "git statusx") {
		t.Fatal("git status should not match git statusx")
	}
	if !matchBashRule(Rule{Kind: "bash", Command: "git status*", Decision: DecisionAllow}, "git status --short") {
		t.Fatal("legacy Command wildcard should remain supported")
	}
}

func TestEvaluateBashRulesCompositeCommands(t *testing.T) {
	rules := []Rule{
		{Kind: "bash", Pattern: "git status", Decision: DecisionAllow},
		{Kind: "bash", Pattern: map[string]string{"regex": `^\s*rm\b`}, Decision: DecisionDeny},
	}
	for _, operator := range []string{"&&", "||", ";", "|"} {
		decision, ok := evaluateBashRules(rules, "git status "+operator+" rm -rf dist")
		if !ok || decision != DecisionDeny {
			t.Fatalf("expected composite command with %q to deny, got %s ok=%v", operator, decision, ok)
		}
	}
	echoRules := []Rule{
		{Kind: "bash", Pattern: "echo", Decision: DecisionAllow},
		{Kind: "bash", Pattern: map[string]string{"regex": `\brm\b`}, Decision: DecisionDeny},
	}
	decision, ok := evaluateBashRules(echoRules, `echo "safe && rm -rf dist"`)
	if !ok || decision != DecisionAllow {
		t.Fatalf("expected quoted && command to allow, got %s ok=%v", decision, ok)
	}
	decision, ok = evaluateBashRules(echoRules, "echo 'safe | rm -rf dist'")
	if !ok || decision != DecisionAllow {
		t.Fatalf("expected quoted pipe command to allow, got %s ok=%v", decision, ok)
	}
}

func TestBashRuleOrderingUsesSuffixFromCurrentRule(t *testing.T) {
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}
	engine := newTestEngine(Options{
		Mode: core.PermissionModeDefault,
		Rules: []Rule{
			{Kind: "bash", Pattern: "rm", Decision: DecisionDeny},
			{Kind: "tool", Tool: "NoMatch", Decision: DecisionAllow},
			{Kind: "bash", Pattern: "git status", Decision: DecisionAllow},
		},
	})
	if got := engine.Evaluate(call("toolu_1", bashTool, map[string]interface{}{"command": "git status --short"})).Decision; got != DecisionAllow {
		t.Fatalf("expected later bash allow to match from current rule suffix, got %s", got)
	}
}

func TestCanUseToolCallback(t *testing.T) {
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}
	input := map[string]interface{}{"command": "git status"}
	var captured CanUseToolRequest
	engine := newTestEngine(Options{
		Mode: core.PermissionModeDefault,
		CanUseTool: func(ctx context.Context, req CanUseToolRequest) (CanUseToolResponse, error) {
			captured = req
			return CanUseToolResponse{Behavior: "allow"}, nil
		},
	})
	decision := engine.Resolve(context.Background(), call("toolu_1", bashTool, input))
	if decision.Decision != DecisionAllow {
		t.Fatalf("expected callback allow, got %s", decision.Decision)
	}
	expected := CanUseToolRequest{
		ToolName:  "Bash",
		ToolUseID: "toolu_1",
		Input:     input,
		Summary:   "Bash",
		Mode:      core.PermissionModeDefault,
	}
	if !reflect.DeepEqual(captured, expected) {
		t.Fatalf("callback request mismatch\nexpected: %#v\nactual:   %#v", expected, captured)
	}
}

type permissionObserver struct {
	mu           sync.Mutex
	observations []core.Observation
}

func (o *permissionObserver) Observe(ctx context.Context, observation core.Observation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observations = append(o.observations, observation)
}

func TestCanUseToolCallbackObservationIncludesDuration(t *testing.T) {
	observer := &permissionObserver{}
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}
	engine := newTestEngine(Options{
		Observer: observer,
		CanUseTool: func(ctx context.Context, req CanUseToolRequest) (CanUseToolResponse, error) {
			time.Sleep(time.Millisecond)
			return CanUseToolResponse{Behavior: "allow"}, nil
		},
	})
	call := call("toolu_1", bashTool, map[string]interface{}{"command": "git status"})
	call.SessionID = "s"
	call.RunID = "r"
	decision := engine.Resolve(context.Background(), call)
	if decision.Decision != DecisionAllow {
		t.Fatalf("expected allow, got %#v", decision)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.observations) != 1 {
		t.Fatalf("expected one observation, got %+v", observer.observations)
	}
	observation := observer.observations[0]
	if observation.Type != core.ObservationPermissionCallback || observation.SessionID != "s" || observation.RunID != "r" || observation.ToolName != "Bash" {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	if observation.DurationMS <= 0 {
		t.Fatalf("expected duration to be recorded, got %+v", observation)
	}
}

func TestCanUseToolCallbackReceivesCancelableContext(t *testing.T) {
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := newTestEngine(Options{
		CanUseTool: func(ctx context.Context, req CanUseToolRequest) (CanUseToolResponse, error) {
			close(started)
			<-ctx.Done()
			return CanUseToolResponse{}, ctx.Err()
		},
	})
	done := make(chan Decision, 1)
	go func() {
		done <- engine.Resolve(ctx, call("toolu_1", bashTool, map[string]interface{}{"command": "git status"}))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("permission callback did not start")
	}
	cancel()
	select {
	case decision := <-done:
		if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "permission callback failed or aborted") {
			t.Fatalf("expected callback cancellation denial, got %#v", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("permission callback did not return after context cancellation")
	}
}

func TestCanUseToolFailureModes(t *testing.T) {
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}
	decision := newTestEngine(Options{Mode: core.PermissionModeDefault}).Resolve(context.Background(), call("toolu_1", bashTool, map[string]interface{}{"command": "git status"}))
	if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "canUseTool callback is not configured") {
		t.Fatalf("expected missing callback denial, got %#v", decision)
	}

	decision = newTestEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			return CanUseToolResponse{Behavior: "deny", Message: "user declined"}, nil
		},
	}).Resolve(context.Background(), call("toolu_1", bashTool, map[string]interface{}{"command": "git status"}))
	if decision.Decision != DecisionDeny || decision.Reason != "user declined" {
		t.Fatalf("expected callback deny message, got %#v", decision)
	}

	decision = newTestEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			return CanUseToolResponse{}, errors.New("boom")
		},
	}).Resolve(context.Background(), call("toolu_1", bashTool, map[string]interface{}{"command": "git status"}))
	if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "permission callback failed or aborted") {
		t.Fatalf("expected callback failure denial, got %#v", decision)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	invoked := false
	decision = newTestEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			invoked = true
			return CanUseToolResponse{Behavior: "allow"}, nil
		},
	}).Resolve(ctx, call("toolu_1", bashTool, map[string]interface{}{"command": "git status"}))
	if decision.Decision != DecisionDeny || invoked {
		t.Fatalf("expected pre-canceled context denial without callback, got %#v invoked=%v", decision, invoked)
	}
}

func TestCanUseToolUpdatedInputValidation(t *testing.T) {
	canonicalTool := testTool{
		name:  "Bash",
		scope: core.ToolScopeExec,
		validate: func(raw map[string]interface{}) (map[string]interface{}, error) {
			command, ok := raw["command"].(string)
			if !ok {
				return nil, core.NewToolExecutionError("Bash", "missing command")
			}
			return map[string]interface{}{"command": strings.TrimSpace(command), "canonical": true}, nil
		},
	}
	decision := newTestEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			return CanUseToolResponse{Behavior: "allow", UpdatedInput: map[string]interface{}{"command": "  git status  "}}, nil
		},
	}).Resolve(context.Background(), call("toolu_1", canonicalTool, map[string]interface{}{"command": "git diff"}))
	if decision.Decision != DecisionAllow || !reflect.DeepEqual(decision.UpdatedInput, map[string]interface{}{"command": "git status", "canonical": true}) {
		t.Fatalf("expected canonical updated input, got %#v", decision)
	}

	strictTool := testTool{
		name:  "Bash",
		scope: core.ToolScopeExec,
		validate: func(raw map[string]interface{}) (map[string]interface{}, error) {
			if raw["command"] != "git status" {
				return nil, core.NewToolExecutionError("Bash", "invalid command")
			}
			return raw, nil
		},
	}
	decision = newTestEngine(Options{
		CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
			return CanUseToolResponse{Behavior: "allow", UpdatedInput: map[string]interface{}{"command": "rm -rf dist"}}, nil
		},
	}).Resolve(context.Background(), call("toolu_1", strictTool, map[string]interface{}{"command": "git diff"}))
	if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "updated input is invalid") {
		t.Fatalf("expected invalid updated input denial, got %#v", decision)
	}
}

func TestCanUseToolMalformedResponsesDenied(t *testing.T) {
	bashTool := testTool{name: "Bash", scope: core.ToolScopeExec}
	responses := []CanUseToolResponse{
		{},
		{Behavior: "maybe"},
		{Behavior: "deny"},
	}
	for _, response := range responses {
		response := response
		decision := newTestEngine(Options{
			CanUseTool: func(context.Context, CanUseToolRequest) (CanUseToolResponse, error) {
				return response, nil
			},
		}).Resolve(context.Background(), call("toolu_1", bashTool, map[string]interface{}{"command": "git status"}))
		if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "permission callback returned an invalid response") {
			t.Fatalf("expected malformed response denial for %#v, got %#v", response, decision)
		}
	}
}

func TestInvalidPendingCallsDenied(t *testing.T) {
	decision := newTestEngine(Options{}).Evaluate(PendingCall{ToolUseID: "", Tool: testTool{name: "Read"}, Input: map[string]interface{}{}})
	if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "tool_use_id") {
		t.Fatalf("expected invalid tool_use_id denial, got %#v", decision)
	}
	decision = newTestEngine(Options{}).Evaluate(PendingCall{ToolUseID: "toolu_1", Tool: nil, Input: map[string]interface{}{}})
	if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "tool is invalid") {
		t.Fatalf("expected invalid tool denial, got %#v", decision)
	}
	decision = newTestEngine(Options{}).Evaluate(PendingCall{ToolUseID: "toolu_1", Tool: testTool{name: "Read"}, Input: nil})
	if decision.Decision != DecisionDeny || !strings.Contains(decision.Reason, "input must be an object") {
		t.Fatalf("expected invalid input denial, got %#v", decision)
	}
}
