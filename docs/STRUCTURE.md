# Go SDK Structure

This Go module is organized around the same public concepts as the original
TypeScript SDK while using normal Go package boundaries. It intentionally keeps
the main SDK package at the module root, which is the standard layout for a Go
library meant to be imported as `github.com/ZekromNguyen/skawld-sdk-go`.

```text
skawld-sdk-go/
  agent.go, session.go, loop.go
    Public root package. Owns Agent, Session, Run, event streaming, and
    orchestration across providers, tools, permissions, and session stores.

  doc.go
    Root package documentation shown by `go doc`.

  core/
    Shared types: messages, content blocks, events, errors, provider contracts,
    tool contracts, session-store contracts, task types.

  internal/
    Private implementation helpers that should not be imported by SDK users.
    Current helpers:
      internal/id/   UUID-like id generation

  providers/
    Normalized provider adapters. Current files cover Anthropic Messages,
    OpenAI Chat Completions, OpenAI Responses, and shared SSE helpers.

  tools/
    Built-in tools and registry. Includes filesystem tools, shell execution,
    search tools, and task tools.

  tools/mcp/
    Target home for MCP client, server config, naming, result translation, and
    MCP-backed Tool implementations.

  permissions/
    Permission engine, rule matching, permission modes, and can-use-tool callback
    support.

  sessions/
    Session persistence implementations. Includes the default in-memory store.

  sessions/sqlite/
    SQLite-backed SessionStore parity with the TypeScript SDK.

  skills/
    Target home for SKILL.md loading, frontmatter parsing, listing generation,
    shell argument splitting, substitution, overlay handling, and Skill tool
    integration.

  subagents/
    Agent-definition loading, registry construction, default subagent, and
    subagent runner integration.

  config/
    JSON config schema and loader support.

  examples/
    Runnable examples.

  docs/
    Migration notes and structure documentation.

  Makefile
    Convenience wrappers for common Go commands: `make test`, `make fmt`,
    `make vet`, and `make tidy`.
```

## Public Package Intent

- `github.com/ZekromNguyen/skawld-sdk-go`: the ergonomic main import for most users.
- `github.com/ZekromNguyen/skawld-sdk-go/providers`: provider constructors.
- `github.com/ZekromNguyen/skawld-sdk-go/tools`: registry and built-in tools.
- `github.com/ZekromNguyen/skawld-sdk-go/sessions`: session stores.
- `github.com/ZekromNguyen/skawld-sdk-go/permissions`: permission rules and callbacks.

Packages such as `skills`, `subagents`, `config`, `tools/mcp`, and
`sessions/sqlite` are implemented migration targets. Remaining known gaps are
tracked in `TODO.md` and `docs/RELEASE_CHECKLIST.md`.

## Why There Is No `pkg/`

The `pkg/` directory is optional in Go and is usually useful for repositories
with many binaries or multiple unrelated libraries. This repository is a single
SDK module, so the idiomatic structure is simpler:

- root package for the primary library API
- public subpackages for supported extension points
- `internal/` for private helpers
- `examples/` for runnable examples
- `docs/` for design and migration notes
