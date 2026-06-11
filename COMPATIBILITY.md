# Compatibility Notes

This document tracks public API changes made during the production hardening
phases (Sprints 9–14). Refer to this when upgrading from a pre-hardening SDK
version.

## Phase 16: Public API Renames (2026-06-11)

| Old Name | New Name | Location |
|---|---|---|
| `AgentOptions.AgentsDir` | `AgentOptions.SubagentsDir` | `agent.go` |
| `config.File.AgentsDir` | `config.File.SubagentsDir` | `config/config.go` |
| `config.AgentOptions.AgentsDir` | `config.AgentOptions.SubagentsDir` | `config/config.go` |

The JSON config key `agents_dir` remains unchanged. Only the Go field name changed.

### Internal Renames (no public API impact)

| Old Name | New Name | Location |
|---|---|---|
| `Session.providerView` | `Session.providerHistory` | `session.go` |
| `Session.fullHistory` | `Session.completeHistory` | `session.go` |
| `Session.compactProviderView()` | `Session.compactProviderHistory()` | `session.go` |

## Phase 10: RunHandle API

The `RunHandle` API replaced raw event channels returned from `Session.Run()`.
`RunHandle` provides `Events()`, `Abort()`, and `Close()` methods.

Migration:
```go
// Before (pre-hardening)
events := session.Run(ctx, prompt, opts)
for ev := range events { ... }

// After (post-hardening)
handle := session.StartRun(ctx, prompt, opts)
defer handle.Close()
for ev := range handle.Events() { ... }
```

## Phase 11: HTTP Client Injection

Providers and MCP clients now accept custom `*http.Client` via options:
- `providers.AnthropicOptions.Client`
- `providers.OpenAIOptions.Client`
- `mcp.ServerConfig.HTTP.Client`

## Phase 12: Context-Aware Stores

`core.SessionStore` interface methods now require `context.Context` as their
first argument. Custom store implementations must update their signatures.

## Phase 13: Filesystem Policy

`AgentOptions.FilesystemPolicy` allows restricting filesystem tools to approved
roots. The default policy allows reads from the working directory and writes to
the working directory.

## Phase 16: Config ProviderFactory

`config.File.AgentOptions(ctx)` still works but delegates to
`AgentOptionsWithFactory(ctx, factory)`. Use `LoadAgentOptionsWithFactory`
to inject a custom factory for testing without concrete provider imports.