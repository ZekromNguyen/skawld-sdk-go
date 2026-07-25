# Architecture Reorganization Plan

This document records a full layout review of the Go Agent SDK and a staged
reorganization plan. The current module is already close to an idiomatic Go SDK:
the module root is the primary public package, domain packages are split by
responsibility, and `internal/` is used for private helpers. The main risk is not
missing directories; it is moving public packages in a way that breaks import
paths for SDK users.

## 1. Current Issues

- Root package owns too many runtime concerns: agent construction, session state,
  run loop, compaction, skill overlays, subagent orchestration, and system prompt
  construction all live in `package skawld`.
- `core/` mixes stable public contracts with storage/task domain models. This is
  acceptable for a small SDK, but it will grow into a broad shared-kernel package
  if workflows, memory, and transports expand.
- `sessions/` is the memory package in practice. The package name is workable,
  but `memory/` would describe the domain more clearly if the SDK adds vector
  memory, summaries, or durable stores.
- `tools/mcp/` combines MCP transport/client behavior with MCP-backed tool
  adaptation. This is convenient today, but transport growth should not happen
  under `tools/`.
- Provider adapters include wire translation, HTTP/SSE transport, error mapping,
  and provider constructors in the same package. That is fine for the public API,
  but internal transport helpers should be private as more providers are added.
- There is no first-class workflow package yet. Current task tools provide task
  state, but there is no workflow orchestration domain.
- There is no observability package. Events provide a good base, but logging,
  tracing, and metrics hooks are not isolated as extension points.
- Top-level integration tests were mixed with package-local white-box tests.
  Black-box public API and MCP integration tests now live under `test/`.

## 2. Recommended Folder Structure

Use this as the target layout. For v1 compatibility, keep current public import
paths and stage internal moves behind aliases or facade packages.

```text
skawld-sdk-go/
  agent.go
  session.go
  exports.go
  doc.go
  cmd/
    skawld/
  api/
    openapi/
    schemas/
  core/
  agents/
    definitions/
    runtime/
  tools/
    builtin/
    registry/
    mcp/
  workflows/
  memory/
    inmem/
    sqlite/
  transport/
    http/
    sse/
    mcp/
    stdio/
  providers/
    anthropic/
    openai/
  middleware/
    permissions/
    observability/
  config/
  internal/
    id/
    runtime/
    compaction/
    prompt/
  examples/
  test/
  docs/
  scripts/
```

## 3. File Relocation Plan

### Safe in v1

| Current file | Target | Reason |
|---|---|---|
| `api_test.go` | `test/api_test.go` | Public black-box API test. Done. |
| `mcp_integration_test.go` | `test/mcp_integration_test.go` | Public MCP integration test. Done. |
| `TestExamplesBuild` in `agent_test.go` | `test/examples_test.go` | Example build validation is integration coverage. Done. |
| `.gocache/`, `.gomodcache/` | ignored | Local build/module cache output. Done. |
| `providers/sse.go` | `internal/transport/sse/sse.go` | Private SSE/HTTP helper once providers call internal transport. |
| `system_prompt.go` | `internal/prompt/system.go` | Prompt construction is runtime implementation. |
| unexported run-loop helpers in `loop.go` | `internal/runtime/` | Keeps public root package thin while preserving exported facade. |
| unexported compaction helpers in `compaction.go` | `internal/compaction/` | Keep public `CompactionStrategy` types in root, move helpers private. |

### Breaking or v2-only

| Current path | Target | Compatibility note |
|---|---|---|
| `sessions/` | `memory/inmem/` | Breaks `github.com/ZekromNguyen/skawld-sdk-go/sessions`. Keep alias package in v1. |
| `sessions/sqlite/` | `memory/sqlite/` | Breaks SQLite import path. Keep alias package in v1. |
| `permissions/` | `middleware/permissions/` | Breaks public rules package. Keep current path unless v2. |
| `tools/mcp/` | `transport/mcp/` plus `tools/mcp/` adapter | Split client transport from tool adapter gradually. |
| `subagents/` | `agents/definitions/` | Public package rename. Prefer adding `agents/` facade first. |
| `skills/` | `agents/skills/` or keep `skills/` | Only move if skills become agent runtime concern. |
| `providers/*.go` | `providers/openai`, `providers/anthropic` | Breaks provider constructors. Add subpackages before deprecating flat package. |

Do not move white-box tests such as `agent_test.go`, `compaction_test.go`,
`skills_integration_test.go`, `subagents_integration_test.go`, or package-local
tests under `tools/`, `providers/`, `config/`, `sessions/`, `skills/`, and
`subagents/` into `test/` unless they are rewritten as external tests. They rely
on unexported helpers or same-package behavior, and colocated tests are the Go
standard for package unit tests.

## 4. Package Refactoring Plan

- Keep `github.com/ZekromNguyen/skawld-sdk-go` as the primary SDK facade. This is the
  idiomatic layout for a single-library Go module.
- Keep `core` small and stable. Long term, split storage models into `memory`
  only if `core` becomes a dumping ground.
- Add `agents` only when there is a public agent-definition API. Until then,
  `subagents` is clearer and avoids vague package names.
- Add `workflows` when there are durable workflow primitives beyond task tools.
  Do not create an empty package for aspiration only.
- Keep package names short, lowercase, and singular where practical:
  `config`, `core`, `tools`, `providers`, `permissions`, `skills`, `subagents`.
- Avoid a top-level `pkg/` for this repository. `pkg/` is optional and usually
  redundant for a single SDK module whose root package is the main library.
- Avoid an `api/` package unless it holds generated protocol schemas or stable
  external API descriptions. Go users should import the root SDK API.

## 5. Architecture Improvements

- Introduce an internal runtime boundary:
  `internal/runtime` should own turn scheduling, provider stream consumption,
  tool scheduling, permission batching, retries, and abort handling.
- Split public compaction contracts from implementation:
  keep `CompactionStrategy`, `CompactionRequest`, `CompactionResult`, and
  `KeepLastTurnsCompactionStrategy` public; move private render/clone/tag helpers
  under `internal/compaction`.
- Split MCP into transport and tool adapter:
  transport code should own stdio/HTTP JSON-RPC clients; `tools/mcp` should adapt
  discovered remote tools to `core.Tool`.
- Add explicit observability hooks:
  provide a small interface for event sinks, logger injection, and metric hooks
  instead of coupling all observability to the event channel.
- Add workflow primitives only when behavior exists:
  task tools are storage-backed state management today. A workflow package should
  appear when the SDK has workflow definitions, steps, transitions, and execution
  policies.
- Make dependency direction explicit:
  `root facade -> internal runtime -> core/contracts -> integrations`. Public
  integrations should depend on `core`; `core` should not depend on integrations.

## 6. Go Best Practice Recommendations

- Preserve current import paths for a v1 SDK. Moving public packages is a
  breaking change in Go because package paths are part of the API.
- Keep package-local unit tests beside implementation files. Move only black-box
  integration tests to `test/`.
- Use `internal/` for implementation details that SDK users must not import.
- Add interfaces at dependency boundaries, not for every struct. Existing
  `Provider`, `Tool`, and `SessionStore` contracts are appropriate.
- Keep `cmd/` only for real binaries. Do not create it unless a CLI is added.
- Use constructor options by value as the repo does today, and keep defaults in
  constructors rather than spread across callers.
- Avoid circular dependencies by keeping shared types in `core` and making
  integrations depend inward.
- Keep generated or local cache directories ignored and out of the module tree.

## 7. Final Project Layout

The practical v1 layout after this change is:

```text
skawld-sdk-go/
  agent.go
  session.go
  loop.go
  compaction.go
  skills_runtime.go
  subagents_runtime.go
  system_prompt.go
  exports.go
  config/
  core/
  docs/
    ARCHITECTURE_REORGANIZATION.md
    STRUCTURE.md
    USAGE.md
    RELEASE_CHECKLIST.md
  examples/
  internal/
    id/
  permissions/
  providers/
  sessions/
    sqlite/
  skills/
  subagents/
  test/
    api_test.go
    examples_test.go
    mcp_integration_test.go
  tools/
    mcp/
```

The recommended v2 layout, if breaking import paths are acceptable, is:

```text
skawld-sdk-go/
  cmd/
  api/
  agents/
  core/
  internal/
  memory/
  middleware/
  providers/
  tools/
  transport/
  workflows/
  config/
  examples/
  test/
  docs/
```

## 8. Migration Steps

1. Keep the current public packages and add `test/` for black-box integration
   tests. This is complete for public API, MCP integration, and example builds.
2. Move private root-package helpers into `internal/` behind root-package wrapper
   functions. Run `go test ./...` after each small move.
3. Split MCP transport internals from tool adapters while keeping
   `tools/mcp` as the public compatibility package.
4. Add new public packages only when they expose real stable extension points:
   `workflows` for workflow orchestration, `middleware/observability` for hooks,
   and `agents` for public agent-definition APIs.
5. For any v2 package rename, add temporary compatibility aliases in the old
   package, update examples and docs, then deprecate old paths before removal.
6. Update CI to keep running `gofmt`, `go vet ./...`, and `go test ./...`.
7. Treat the root package as a facade: keep user ergonomics there, but move
   implementation gravity into cohesive internal packages over time.
