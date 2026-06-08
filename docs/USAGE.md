# Usage Notes

## Provider Setup

Use `providers.NewOpenAIResponsesProvider`, `providers.NewOpenAIChatCompletionsProvider`, or `providers.NewAnthropicProvider`.

The OpenAI providers read `OPENAI_API_KEY` when `OpenAIOptions.APIKey` is empty. The Anthropic provider reads `ANTHROPIC_API_KEY` when `AnthropicOptions.APIKey` is empty. `BaseURL` and `DefaultHeaders` are available for compatible gateways.

## Run Lifecycle

`Session.Run` remains the channel-based compatibility API. When callers may stop reading before the run finishes, use `Session.StartRun` and close the handle:

```go
handle := session.StartRun(ctx, "Inspect this repository.", skawld.RunOptions{})
defer handle.Close()

for event := range handle.Events() {
	if event.Type == skawld.EventResult {
		break
	}
}
```

`RunHandle.Abort()` cancels provider and tool work while still allowing the run to emit an aborted result. `RunHandle.Close()` is for abandoned consumers and cancels event delivery so active-run state and provider streams can unwind.

## Custom Tools

Implement `core.Tool` or the root `skawld.Tool` alias:

```go
type MyTool struct{}

func (MyTool) Name() string { return "MyTool" }
func (MyTool) Description() string { return "Do one focused operation." }
func (MyTool) InputSchema() map[string]interface{} { return map[string]interface{}{"type": "object"} }
func (MyTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (MyTool) ParallelSafe() bool { return true }
func (MyTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) { return raw, nil }
func (MyTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{Content: "ok", Summary: "ok"}, nil
}
func (MyTool) Summarize(input map[string]interface{}) string { return "Run MyTool" }
```

Register tools with `tools.NewRegistry()` or `tools.DefaultTools()`.

## Permission Callbacks

Set `PermissionOptions.CanUseTool` to approve, deny, or rewrite a pending tool call. Read-scoped tools are allowed by default. Write and exec tools ask in `default` mode unless rules or a callback allow them.

```go
Permissions: skawld.PermissionOptions{
	Mode: skawld.PermissionModeDefault,
	CanUseTool: func(ctx context.Context, req permissions.CanUseToolRequest) (permissions.CanUseToolResponse, error) {
		return permissions.CanUseToolResponse{Behavior: "allow"}, nil
	},
}
```

Permission callbacks are called synchronously and receive the run context. They should return promptly when `ctx` is canceled.

## Observability

Set `AgentOptions.Logger` for structured `slog` records, or `AgentOptions.Observer` for metrics/tracing callbacks. Observations include stable fields such as session ID, run ID, provider ID, tool name, attempt number, duration, retryability, and error kind.

Observer and logger payloads do not include raw prompts, provider request bodies, tool inputs, HTTP headers, API keys, or large tool results by default.

## SQLite Sessions

Use `sessions/sqlite` for persistent sessions, messages, tasks, task dependencies, and invoked skills:

```go
store, err := sqlite.Open("skawld.db")
if err != nil {
	return err
}
defer store.Close()

agent, err := skawld.NewAgent(skawld.AgentOptions{
	SessionStore: store,
	// provider, model, tools, permissions...
})
```

Reusing a `SessionOptions.ID` resumes stored messages for that session.
