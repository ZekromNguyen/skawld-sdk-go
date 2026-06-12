# Skawld Agent SDK for Go

A Go-native agent SDK for building AI coding assistants. Provides an embeddable
Agent runtime with streaming providers, built-in tools, permissions, session
persistence, skills, subagents, and MCP tool integration — plus a premium
terminal UI (Raven CLI) that consumes the SDK event stream.

```sh
go test ./...
```

Development checks via the `Makefile`:

```sh
make fmt      # gofmt -w .
make vet      # go vet ./...
make test     # go test ./...
make tidy     # go mod tidy
```

## Quick start

```go
package main

import (
    "context"
    "fmt"
    "os"

    skawld "github.com/skawld/skawld-sdk-go"
    "github.com/skawld/skawld-sdk-go/providers"
    "github.com/skawld/skawld-sdk-go/tools"
)

func main() {
    agent, err := skawld.NewAgent(skawld.AgentOptions{
        Provider: providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{}),
        Model:    "gpt-5",
        Tools:    tools.DefaultTools(),
        Permissions: skawld.PermissionOptions{
            Mode: skawld.PermissionModeDefault,
        },
    })
    if err != nil {
        panic(err)
    }
    defer agent.Close()

    session, err := agent.Session(context.Background(), skawld.SessionOptions{})
    if err != nil {
        panic(err)
    }

    for event := range session.Run(context.Background(), "List files in the current directory.", skawld.RunOptions{}) {
        if event.Type == skawld.EventAssistant {
            for _, block := range event.Message.Content {
                if block.Type == skawld.BlockText {
                    fmt.Fprint(os.Stdout, block.Text)
                }
            }
        }
    }
}
```

## Raven CLI

Raven is a premium terminal UI for the skawld SDK. It renders the event stream
with a thoughtfully designed TUI: streaming text, tool execution display,
interactive permission prompts, and session management.

```sh
# Install
go install ./cmd/raven

# Interactive REPL with splash screen
raven

# Single-shot mode
raven --prompt "Fix the auth middleware"

# Resume a session
raven --session <id> --prompt "Continue the review"

# Override model
raven --model claude-haiku-4-5
```

**Raven features:**

| Feature | Description |
|---------|-------------|
| Welcome screen | Animated raven silhouette splash on launch |
| Streaming text | Real-time token-by-token rendering with blink cursor |
| Tool display | Icon-labelled tool executions with live durations |
| Diff preview | Side-by-side diff rendering for Edit/Write tools |
| Permission dialogs | Inline modal with Y/A/N/S choices and diff preview |
| Command palette | Ctrl+P fuzzy command picker |
| Slash commands | `/help`, `/model`, `/clear`, `/status`, `/sessions`, `/memory`, `/settings`, `/cost`, `/export` |
| Status bar | Model, token usage, cost, MCP indicator, help hint |
| Line editing | Readline-style input with history, Ctrl+W/U/K, multi-line |
| Toast notifications | Top-right transient success/error/warning toasts |
| Resize | Auto-adapts to terminal dimensions |
| Raw mode | Direct keyboard input: arrows, Home/End, Ctrl+A/E, etc. |

**Keyboard shortcuts (interactive mode):**

| Key | Action |
|-----|--------|
| Ctrl+P | Command palette |
| Ctrl+C | Cancel current operation |
| Ctrl+D | Exit Raven |
| Ctrl+L | Clear screen |
| Up/Down | Navigate history |
| `/` | Slash commands |

## Package map

| Package | Purpose |
|---------|---------|
| `github.com/skawld/skawld-sdk-go` | Root: `Agent`, `Session`, `RunHandle`, events, core aliases, errors |
| `github.com/skawld/skawld-sdk-go/providers` | Anthropic Messages, OpenAI Chat Completions, OpenAI Responses providers |
| `github.com/skawld/skawld-sdk-go/tools` | `Registry`, `DefaultTools`, built-in tools (Read, Write, Edit, Bash, Glob, Grep, Task*) |
| `github.com/skawld/skawld-sdk-go/tools/mcp` | MCP client, server configs (stdio + HTTP), MCP Tool wrapper |
| `github.com/skawld/skawld-sdk-go/permissions` | `Engine`, rule matching, permission modes, `CanUseTool` callback |
| `github.com/skawld/skawld-sdk-go/sessions` | In-memory `SessionStore` |
| `github.com/skawld/skawld-sdk-go/sessions/sqlite` | Persistent SQLite-backed `SessionStore` |
| `github.com/skawld/skawld-sdk-go/skills` | SKILL.md loader, frontmatter parsing, shell argument substitution, Skill tool |
| `github.com/skawld/skawld-sdk-go/subagents` | Agent-definition loader, registry, Subagent tool |
| `github.com/skawld/skawld-sdk-go/config` | JSON config schema and loader |
| `github.com/skawld/skawld-sdk-go/core` | Shared types: messages, content blocks, events, provider/tool/store contracts |
| `github.com/skawld/skawld-sdk-go/internal/` | Private helpers (ID generation, frontmatter parser, SSE parser) |
| `github.com/skawld/skawld-sdk-go/cmd/raven` | Raven CLI — premium terminal UI |
| `github.com/skawld/skawld-sdk-go/examples/minimal` | Minimal one-shot agent example |
| `github.com/skawld/skawld-sdk-go/examples/interactive_cli` | Interactive chat-loop example |
| `github.com/skawld/skawld-sdk-go/examples/mcp_agent` | Agent with MCP server integration |

## Provider setup

Three provider types ship in `providers/`:

```go
// Anthropic Messages API
providers.NewAnthropicProvider(providers.AnthropicOptions{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
})

// OpenAI Responses API
providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{
    APIKey: os.Getenv("OPENAI_API_KEY"),
})

// OpenAI Chat Completions API
providers.NewOpenAIChatCompletionsProvider(providers.OpenAIOptions{
    APIKey: os.Getenv("OPENAI_API_KEY"),
})
```

Providers read their respective `*_API_KEY` environment variable when the
`APIKey` field is empty. `BaseURL` and `DefaultHeaders` are available for
compatible gateways and proxies.

## Run lifecycle

Prefer `StartRun` / `RunHandle` for safe cancellation and cleanup:

```go
handle := session.StartRun(ctx, "Inspect this repository.", skawld.RunOptions{})
defer handle.Close()

for event := range handle.Events() {
    switch event.Type {
    case skawld.EventAssistant:
        // render text, thinking, tool calls from event.Message.Content
    case skawld.EventToolCallStart:
        // tool execution started
    case skawld.EventToolCallEnd:
        // tool finished — check event.IsError and event.DurationMS
    case skawld.EventPermissionRequest:
        // must decide allow/deny — see engine callbacks
    case skawld.EventUsage:
        // cumulative token + cost snapshot
    case skawld.EventCompaction:
        // context was compacted
    case skawld.EventResult:
        // run completed — check event.Subtype: "success" | "error" | "aborted"
    }
}
```

`RunHandle.Abort()` cancels provider and tool work while still emitting an
aborted result. `RunHandle.Close()` is for abandoned consumers — it cancels
event delivery so active-run state and provider streams can unwind.

## Custom tools

Implement `core.Tool` (aliased as `skawld.Tool` in the root package):

```go
type MyTool struct{}

func (MyTool) Name() string        { return "MyTool" }
func (MyTool) Description() string { return "Do one focused operation." }
func (MyTool) InputSchema() map[string]interface{} {
    return map[string]interface{}{"type": "object"}
}
func (MyTool) Scope() core.ToolScope       { return core.ToolScopeRead }
func (MyTool) ParallelSafe() bool          { return true }
func (MyTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
    return raw, nil
}
func (MyTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
    return core.ToolResult{Content: "ok", Summary: "ok"}, nil
}
func (MyTool) Summarize(input map[string]interface{}) string {
    return "Run MyTool"
}
```

Register with `tools.NewRegistry()` or clone `tools.DefaultTools()`:

```go
reg := tools.DefaultTools()
reg.Register(MyTool{})
agent, _ := skawld.NewAgent(skawld.AgentOptions{
    Tools: reg,
    // ...
})
```

## Permissions

Three modes are available:

| Mode | Behavior |
|------|----------|
| `PermissionModeDefault` | Ask before writes and exec; reads auto-allowed |
| `PermissionModeAcceptEdits` | Auto-approve edits, ask before commands |
| `PermissionModeYolo` | Run everything without asking |

Add custom rules or a callback for finer control:

```go
Permissions: skawld.PermissionOptions{
    Mode: skawld.PermissionModeDefault,
    Rules: []permissions.Rule{
        {ToolName: "Bash", Allow: true, RequireApproval: true},
    },
    CanUseTool: func(ctx context.Context, req permissions.CanUseToolRequest) (permissions.CanUseToolResponse, error) {
        // approve, deny, or rewrite tool input
        return permissions.CanUseToolResponse{Behavior: "allow"}, nil
    },
}
```

## SQLite sessions

Persistent sessions with full message history:

```go
import "github.com/skawld/skawld-sdk-go/sessions/sqlite"

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

## MCP tools

Connect to MCP servers (stdio or HTTP) for additional tools:

```go
opts := skawld.AgentOptions{
    // ...
    MCPServers: []mcp.ServerConfig{
        {
            Name: "filesystem",
            Stdio: &mcp.StdioServerConfig{
                Command: "npx",
                Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
            },
        },
        {
            Name: "remote",
            HTTP: &mcp.HTTPServerConfig{
                URL: "https://mcp.example.com",
            },
        },
    },
}
```

## Skills and subagents

Place SKILL.md files in `.skawld/skills/` and agent definitions in
`.skawld/agents/`. They auto-load at session start. Disable via
`DisableSkills: true` or `DisableSubagents: true`.

Skills support shell argument substitution, overlay handling, and a built-in
`Skill` tool. Subagents run with their own provider instance and model
configuration.

## Compaction

Automatic context compaction triggers when estimated token usage exceeds the
threshold (default 80% of context window). Emits `EventCompaction` events with
before/after message and token counts.

```go
opts := skawld.AgentOptions{
    CompactionThreshold: 0.8,           // trigger at 80% (default)
    DisableCompaction:   false,
    // CompactionStrategy: &MyStrategy{},  // custom strategy
}
```

## Observability

Set a structured logger or observer for metrics and tracing:

```go
opts := skawld.AgentOptions{
    Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)),
    // or
    Observer: myObserver{},
}
```

Observations include stable fields: session ID, run ID, provider ID, tool name,
attempt number, duration, retryability, and error kind. Raw prompts, request
bodies, tool inputs, and API keys are excluded by default.

Observable lifecycle spans:
- `ObservationProviderAttempt` — each provider HTTP call
- `ObservationToolExecution` — each tool invoke
- `ObservationPermissionCallback` — permission decisions
- `ObservationCompaction` — compaction runs
- `ObservationMCPCall` — MCP connect/discover
- `ObservationStoreOperation` — store reads/writes

## Directory structure

```text
skawld-sdk-go/
  agent.go                Agent construction and lifecycle
  session.go              Session, RunHandle, run management
  loop.go                 Run loop: event dispatch, tool execution, compaction
  compaction.go           CompactionStrategy and default implementation
  exports.go              Public type aliases (ModelID, Event, Tool, etc.)
  config_adapter.go       Adapts config.File to AgentOptions
  observability.go        Observation types and helpers
  store_observer.go       Session-store observation wrapper
  skills_runtime.go       Skill invocation runtime
  subagents_runtime.go    Subagent invocation runtime
  system_prompt.go        System prompt construction

  core/                   Shared types and contracts
  providers/              Provider implementations (Anthropic, OpenAI)
  tools/                  Built-in tools and registry
  tools/mcp/              MCP client and tool integration
  permissions/            Permission engine
  sessions/               In-memory session store
  sessions/sqlite/        SQLite-backed session store
  skills/                 SKILL.md loader and Skill tool
  subagents/              Agent-definition loader and Subagent tool
  config/                 JSON config schema and loader
  internal/               Private helpers (id, sse, frontmatter, jsoncopy)

  cmd/raven/              Raven CLI terminal UI
    main.go                 Entry point and REPL loop
    internal/tui/
      screen.go             Terminal management (raw mode, alt screen, resize)
      buffer.go             Double-buffered diffing renderer
      theme.go              ANSI-256 color theme with NO_COLOR support
      ansi.go               ANSI escape sequences, box drawing, progress bars
      renderer.go           Event dispatch → ChatView, StatusView, ToolsView
      welcome.go            Raven ASCII splash screen with animation
      input.go              Raw input reader, CSI parser, line editor
      dialogs.go            Command palette, permission dialog
      modals.go             Model picker, settings, sessions, memories, setup
                            wizard, export dialog, cost breakdown, agent view,
                            theme switcher, toast notifications
      diff.go               Unified diff engine and diff rendering

  examples/               Runnable examples
  docs/                   Usage, structure, and release notes
  Makefile                Development shortcuts
```

## Known gaps

See `docs/RELEASE_CHECKLIST.md` and `TODO.md` for tracked open items. Notable
areas still in progress:

- Abandoned run iterator cleanup semantics
- Memory session parity with TypeScript SDK
- Permissions/tool parity rollups for remaining TypeScript fixtures

## License

MIT