# Package Layout — Post-Hardening

This document describes the Go SDK package structure after the production
hardening phases (Sprints 9–14). It is the reference for contributors who
need to understand package boundaries, internal conventions, and dependency
rules.

## Top-Level Packages

| Package | Purpose | Dependencies |
|---|---|---|
| `skawld` (root) | Public API surface: Agent, Session, RunHandle, events, compaction, skills/subagent runtime, observability. Re-exports selected core types. | `core`, `tools`, `tools/mcp`, `skills`, `subagents`, `sessions`, `permissions`, `config`, `internal/*` |
| `core` | Domain types and interfaces: Event, Message, ContentBlock, Provider, Tool, SessionStore, Observer, errors. Zero external dependencies. | (none) |
| `tools` | Built-in tool implementations (Read, Write, Edit, Glob, Grep, Bash, Tasks) plus Registry, typed input parsers, and filesystem policy. | `core` |
| `tools/mcp` | MCP client lifecycle, transports (stdio/HTTP), config types, tool wrapper. | `core`, `internal/sse` |
| `providers` | LLM provider adapters: Anthropic Messages, OpenAI Chat Completions, OpenAI Responses. SSE streaming transport. | `core`, `internal/sse` |
| `config` | JSON config file parsing and validation. Provider construction via ProviderFactory. | `core`, `tools/mcp` (for ServerConfig) |
| `sessions` | Session store implementations: in-memory and SQLite. | `core`, `internal/id`, `internal/jsoncopy` |
| `sessions/sqlite` | SQLite-backed session store. | `core`, `sessions` |
| `skills` | Skill definition loading from SKILL.md files. Skill tool. | `core`, `internal/frontmatter` |
| `subagents` | Subagent definition loading from `.md` files. Subagent tool. | `core`, `internal/frontmatter` |
| `permissions` | Permission engine: rule evaluation, modes (default/acceptEdits/yolo), request batching. | `core` |
| `internal/sse` | Shared bounded SSE parser. Used by providers and MCP. | (stdlib only) |
| `internal/frontmatter` | Shared YAML-like frontmatter parser. Used by skills and subagents. | (stdlib only) |
| `internal/jsoncopy` | Deep-copy helpers for SessionRecord, Messages, ContentBlocks, Tasks. Used by sessions. | `core` |
| `internal/id` | Cryptographically random ID generation. | (stdlib only) |

## Dependency Rules

1. **`core` imports nothing outside stdlib.** It defines the contract that all other packages implement.

2. **`internal/*` packages have no outward visibility.** They exist solely for shared utilities that multiple sibling packages need (SSE, frontmatter, deep copy, IDs).

3. **No cyclic imports.** The DAG is: `core` → `internal/*` → domain packages (`tools`, `providers`, `sessions`, `skills`, `subagents`, `permissions`, `config`) → root `skawld`.

4. **`config` decouples from provider imports via `ProviderFactory`.** The default factory lives in `config/factory.go`. Tests can inject a fake factory without linking provider packages.

## Naming Conventions

| Concept | Name | Location |
|---|---|---|
| Messages sent to the LLM provider | `providerHistory` | `session.go` |
| Full (unstripped) message log | `completeHistory` | `session.go` |
| Directory for subagent `.md` definitions | `SubagentsDir` | `AgentOptions`, `config.File` |
| Directory for SKILL.md definitions | `SkillsDir` | `AgentOptions`, `config.File` |
| Tool registry after runtime loading | `runtimeTools` (internal concept) | `agent.go` |
| Provider factory interface | `ProviderFactory` | `config/factory.go` |

### Public API Renames (Phase 16)

- `AgentOptions.AgentsDir` → `AgentOptions.SubagentsDir`
- `config.File.AgentsDir` → `config.File.SubagentsDir` (JSON tag unchanged: `agents_dir`)
- `config.AgentOptions.AgentsDir` → `config.AgentOptions.SubagentsDir`

### Internal Renames (Phase 16)

- `Session.providerView` → `Session.providerHistory`
- `Session.fullHistory` → `Session.completeHistory`
- `Session.compactProviderView()` → `Session.compactProviderHistory()`

## Common Patterns

### Typed Input Parsers for Tools

Each built-in tool has a corresponding typed input struct in `tools/inputs.go`.
`Validate()` methods delegate to `parse*Input()` and return `parsed.mapValue()`,
which preserves the `core.Tool` interface contract (`map[string]interface{}`)
while giving internal code access to typed fields.

### Frontmatter Parsing

SKILL.md and subagent `.md` files share a single frontmatter parser in
`internal/frontmatter`. The parser supports scalar values, inline bracket
lists (`[a, b]`), and YAML-style block lists (`- item`). Both `skills.LoadFile`
and `subagents.LoadFile` call `frontmatter.ParseDocument`.

### Deep Copy at Store Boundaries

In-memory session store uses `internal/jsoncopy` to ensure callers cannot
mutate stored state through returned maps, slices, or content blocks. All
public store methods return deep-copied values.

### Provider Construction

The `ProviderFactory` interface allows callers to provide their own provider
construction logic without importing concrete provider packages. The default
implementation in `config/factory.go` handles Anthropic, OpenAI Chat, and
OpenAI Responses providers.

## Adding a New Package

1. Define interfaces/types in `core` if they are used across packages.
2. Place new built-in tools in `tools/` or a sub-package of `tools/`.
3. Place shared utilities (parsers, helpers) in `internal/`.
4. Register new packages in the root `skawld` package via `AgentOptions`.
5. Run `go test ./...`, `go vet ./...` and verify no import cycles.

## Integration Tests

- `test/` contains end-to-end integration tests (API smoke, MCP, examples).
- `*_integration_test.go` files in the root package test agent lifecycle with fake providers.
- Example applications live in `examples/`.