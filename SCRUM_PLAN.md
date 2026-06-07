# Scrum Implementation Plan

This plan turns `TODO.md` into phased delivery work. Each phase is designed as
a Scrum-aligned increment: implementation tasks, testing tasks, and acceptance
criteria. The phases are ordered so later work depends on stable foundations
from earlier phases.

Status reviewed against the current Go codebase and tests on 2026-06-04.

## Phase 0: Project Baseline And Backlog Hygiene

Goal: make the Go SDK easy to work on before adding large features.

Implementation tasks:

- [x] Confirm package boundaries for root SDK, `core`, `providers`, `tools`,
      `permissions`, `sessions`, `skills`, `subagents`, `config`, and
      `tools/mcp`.
- [x] Add issue labels or backlog categories matching this phase plan.
- [x] Normalize public names and comments so `go doc ./...` is useful.
- [x] Add CI commands for `gofmt`, `go vet`, `go test ./...`, and examples.
- [x] Decide the minimum Go version and supported OS targets.

Testing tasks:

- [x] Add an examples build test.
- [x] Add a public API smoke test for root package imports.
- [x] Run `go test ./...`, `go vet ./...`, and `go test ./examples/...`.

Acceptance criteria:

- [x] New contributors can run one documented command to verify the project.
- [x] Package layout is stable enough for feature work.
- [x] No known failing tests in the baseline.

## Phase 1: Core Agent Loop And Event Contract

Goal: make the engine behavior match the TypeScript SDK before expanding
external integrations.

Implementation tasks:

- [x] Add `Session.Abort()` API equivalent to TypeScript `Session.abort()`.
- [x] Reject concurrent active runs and clean up active-run state when streams finish.
- [x] Add cleanup semantics for abandoned run iterators.
- [x] Preserve provider metadata carried on session messages.
- [x] Complete partial assistant event handling for text, thinking, and tool JSON.
- [x] Implement provider retry loop around stream attempts.
- [x] Add typed run errors for abort, provider, config, and turn-limit cases.

Testing tasks:

- [x] Port core loop tests.
- [x] Add abort tests for idle, active, and repeated abort calls.
- [x] Add active-run conflict tests.
- [x] Add partial-event stream assembly tests.
- [x] Add provider metadata persistence tests.
- [x] Add retry success and retry exhaustion tests with fake providers.

Acceptance criteria:

- [x] A single user turn streams the same event order as the TypeScript SDK.
- [x] Aborted runs always end with a result event.
- [x] Provider metadata survives append, resume, and the next provider request.

## Phase 2: Tool Scheduler And Built-In Tools

Goal: bring local tool execution to parity and make tool scheduling reliable.

Implementation tasks:

- [x] Implement adjacent-batch partitioning by `parallelSafe`.
- [x] Add bounded concurrent execution for read-safe tools.
- [x] Add event interleaving for tool-emitted nested events.
- [x] Add synthetic `tool_call_end` events on abort.
- [x] Improve `Read`, `Write`, and `Edit` parity.
- [x] Improve `Glob` and `Grep` parity, including ripgrep fast paths.
- [x] Improve `Bash` parity, including process-tree termination.
- [x] Add basic task create/list/get/update tools backed by the session store.
- [x] Complete task dependency edge support and cycle detection.
- [x] Add task metadata null-delete and deleted-status compatibility.

Testing tasks:

- [x] Port scheduler tests.
- [x] Port filesystem tool tests.
- [x] Port `Glob` and `Grep` tests, including fallback equivalence.
- [x] Port `Bash` timeout and abort tests.
- [x] Port task store/tool tests.
- [x] Add Windows-specific tests where behavior differs.

Acceptance criteria:

- [x] Parallel-safe calls execute concurrently without reordering result blocks.
- [x] Write/exec tools remain serialized.
- [x] Read-before-edit and read-before-overwrite are enforced.
- [x] Filesystem, search, and Bash tools match the currently documented Go behavior.
- [x] Task tools support dependency edges, cycle rejection, metadata key deletion, and deleted status compatibility.

## Phase 3: Permissions And Session Persistence

Goal: make safety decisions and persisted state match the original SDK.

Implementation tasks:

- [x] Match TypeScript permission rule semantics exactly.
- [x] Implement permission mode defaults for `default`, `acceptEdits`, and `yolo`.
- [x] Implement basic tool and path rules.
- [x] Implement bash command rules.
- [x] Implement named tool argument rules.
- [x] Implement path rule matching and ordering parity.
- [x] Implement permission-request batching before callback execution.
- [x] Validate invalid permission callback responses.
- [x] Implement SQLite-backed `SessionStore`.
- [x] Persist sessions, messages, tasks, task edges, and invoked skills.
- [x] Add schema initialization and migration-safe open behavior.

Testing tasks:

- [x] Add permission engine tests for modes, tool/path/bash rules, callback validation, and input rewrites.
- [x] Add input rewrite tests for `CanUseTool`.
- [ ] Port memory session tests.
- [x] Add SQLite session tests.
- [x] Add persistence/resume integration tests.
- [x] Add in-memory task dependency tests.

Acceptance criteria:

- [x] Default, `acceptEdits`, and `yolo` modes have Go implementations.
- [x] Permission prompts are emitted and batched before callbacks run.
- [x] Permission rule behavior is verified against TypeScript.
- [x] SQLite sessions can be closed, reopened, resumed, and listed.

## Phase 4: Provider Parity

Goal: make Anthropic and OpenAI adapters production-ready.

Implementation tasks:

- [x] Ship baseline Anthropic, OpenAI Chat Completions, and OpenAI Responses adapters.
- [x] Harden Anthropic Messages translation for all content block types.
- [x] Add Anthropic prompt caching breakpoints and TTL support.
- [x] Add Anthropic thinking and effort support.
- [x] Add Anthropic usage and typed error mapping.
- [x] Harden OpenAI Chat image and tool-result translation.
- [x] Add OpenAI-compatible base URL, default header, and context-window options.
- [x] Harden OpenAI Responses previous-response replay.
- [x] Capture OpenAI Responses response metadata for replay.
- [x] Add OpenAI Responses reasoning summary and encrypted reasoning support.
- [x] Complete stop reason and retry-after mapping.

Testing tasks:

- [x] Port provider translation tests.
- [x] Add fake HTTP/SSE server tests for Anthropic.
- [x] Add fake HTTP/SSE server tests for OpenAI Chat.
- [x] Add fake HTTP/SSE server tests for OpenAI Responses.
- [x] Add provider error mapping tests.
- [x] Add provider retry-loop tests with fake providers.
- [x] Add provider adapter retry-after tests.

Acceptance criteria:

- [x] Provider requests match the expected wire shape.
- [x] Streaming wire events map into the normalized provider event contract.
- [x] Typed provider errors are stable enough for application code.

## Phase 5: Compaction

Goal: support long-running sessions without losing important context.

Implementation tasks:

- [x] Add `CompactionStrategy` interface.
- [x] Implement default keep-last-10-turns compaction.
- [x] Add summarization provider call.
- [x] Add proactive context-window threshold logic.
- [x] Add forced compaction after context-length errors.
- [x] Re-inject skill listing and invoked skill bodies after compaction.
- [x] Emit `compaction` events with before/after counts.

Testing tasks:

- [x] Port compaction unit tests.
- [x] Add strategy no-op suppression tests.
- [x] Add forced compaction retry tests.
- [x] Add provider-view versus full-history tests.
- [x] Add skill replay after compaction tests once skills are available.

Acceptance criteria:

- [x] Compaction changes only provider view, not full stored history.
- [x] Context-length failures get one forced compaction retry.
- [x] Consumers receive accurate compaction events.

## Phase 6: MCP Integration

Goal: expose external MCP server tools through the Go SDK.

Implementation tasks:

- [x] Implement MCP server config types for stdio and HTTP.
- [x] Implement MCP client lifecycle.
- [x] Connect MCP servers lazily on first `Agent.Session`.
- [x] Register tools as `mcp__server__tool`.
- [x] Add MCP tool result conversion.
- [x] Add naming and collision handling.
- [x] Close MCP resources on `Agent.Close`.

Testing tasks:

- [x] Add MCP unit tests for config and naming.
- [x] Add MCP result conversion tests.
- [x] Add MCP lifecycle tests.
- [x] Add end-to-end echo MCP server test.

Acceptance criteria:

- [x] MCP tools appear in the system event and provider tool schemas.
- [x] Failed first connection can be retried later.
- [x] MCP resources close cleanly.

## Phase 7: Skills

Goal: port SKILL.md loading and one-turn skill overlays.

Implementation tasks:

- [x] Load `.skawld/skills/<name>/SKILL.md`.
- [x] Parse frontmatter fields.
- [x] Build skill listing prompt block.
- [x] Implement Skill tool.
- [x] Implement skill argument substitution.
- [x] Implement additive allowed-tools overlay.
- [x] Implement model override overlay.
- [x] Persist invoked skills.
- [x] Emit skills events.

Testing tasks:

- [x] Port skill loader tests.
- [x] Port listing tests.
- [x] Port substitution and shell-split tests.
- [x] Add Skill tool execution tests.
- [x] Add resume and compaction replay tests.

Acceptance criteria:

- [x] Skills load lazily on first session.
- [x] Informational skills are auto-allowed.
- [x] Skill overlays affect exactly one assistant turn.

## Phase 8: Subagents

Goal: support nested agent runs and hierarchical event streams.

Implementation tasks:

- [x] Load `.skawld/agents/<name>.md` definitions.
- [x] Implement built-in default subagent.
- [x] Implement subagent registry.
- [x] Implement Subagent tool.
- [x] Implement child session runner.
- [x] Filter child tool registry by agent definition.
- [x] Wrap child events as `subagent_event`.
- [x] Support nested subagent events.

Testing tasks:

- [x] Port subagent loader tests.
- [x] Port registry tests.
- [x] Port runner tests.
- [x] Add nested subagent event tests.
- [x] Add subagent end-to-end tests.

Acceptance criteria:

- [x] Parent run streams child events without breaking event ordering.
- [x] Child sessions inherit the correct run options.
- [x] Nested subagents are observable by UI consumers.

## Phase 9: Configuration, Documentation, And Release Readiness

Goal: make the Go SDK consumable by application developers.

Implementation tasks:

- [x] Port config schema types.
- [x] Implement Go config loader and precedence rules.
- [x] Add minimal example.
- [x] Add MCP example.
- [x] Add interactive CLI example.
- [x] Document provider setup and environment variables.
- [x] Document custom tools.
- [x] Document permission callbacks.
- [x] Document SQLite session persistence.
- [x] Add changelog and release checklist.

Testing tasks:

- [x] Add config loader tests.
- [x] Add missing/invalid config tests.
- [x] Add examples build tests.
- [x] Add public API/surface tests.
- [x] Run full parity suite before release.

Acceptance criteria:

- [x] Developers can follow docs to run a minimal agent.
- [x] Examples compile in CI.
- [x] Release checklist clearly states remaining known gaps.

## Suggested Sprint Grouping

Sprint 1:

- [x] Phase 0 baseline
- [x] Phase 1 core event contract

Sprint 2:

- [x] Phase 2 scheduler foundation
- [x] Phase 2 filesystem tools

Sprint 3:

- [x] Phase 2 search/bash tools
- [x] Phase 2 task dependency/metadata tools
- [x] Phase 3 permissions parity
  - [x] Permission modes, bash rules, named argument rules, input rewrites, callback validation, and request batching
  - [x] Exact TypeScript path/rule precedence parity and full permission test port

Sprint 4:

- [x] Phase 3 SQLite sessions
- [x] Phase 4 provider translation tests

Sprint 5:

- [x] Phase 4 provider hardening
- [x] Phase 5 compaction

Sprint 6:

- [x] Phase 6 MCP

Sprint 7:

- [x] Phase 7 skills

Sprint 8:

- [x] Phase 8 subagents
- [x] Phase 9 docs and release readiness

## Production Hardening Scrum Plan

This plan turns the `TODO.md` Production Audit Backlog into phased delivery
work. It continues after the completed migration plan and keeps the same
Scrum-aligned structure: implementation tasks, testing tasks, and acceptance
criteria for each increment.

Status updated after Sprint 11 implementation on 2026-06-07.

## Phase 10: Run Lifecycle And Provider Stream Safety

Goal: eliminate goroutine leaks and deadlock-prone stream behavior in the core
agent loop.

Implementation tasks:

- [x] Add a `RunHandle` API with `Events()`, `Abort()`, and `Close()`.
- [x] Keep `Session.Run` as a compatibility wrapper around the new run handle.
- [x] Add a context-aware event emitter and replace direct `out <- event` sends
      in the run loop, compaction, permissions, tool execution, skills, and
      subagents.
- [x] Ensure active-run state is cleaned up when callers abandon event
      consumption.
- [x] Replace the provider dual-channel stream contract with a single
      pull-based stream or a single result channel.
- [x] Add a compatibility adapter for existing provider implementations during
      the stream contract migration.
- [x] Make Anthropic, OpenAI Chat, and OpenAI Responses provider sends
      context-aware.
- [x] Ensure provider stream retry behavior exits immediately on cancellation.

Testing tasks:

- [x] Add abandoned-consumer tests for `Session.Run`.
- [x] Add tests that stop reading after the first event and verify active-run
      cleanup.
- [x] Add provider stream cancellation tests for partial text, thinking, and
      tool-input deltas.
- [x] Add provider error ordering tests for errors before and after partial
      output.
- [x] Add subagent cancellation tests that verify child events stop when the
      parent run is canceled.
- [x] Run `go test ./...` and targeted leak tests.

Acceptance criteria:

- [x] Abandoned event consumers do not leave a session permanently active.
- [x] Every event emission path can exit through `ctx.Done()`.
- [x] Provider streams have one clear terminal error path.
- [x] Provider adapters cannot block forever on a downstream reader that has
      stopped.

## Phase 11: Transport, MCP, And Process Resource Safety

Goal: make network streams, MCP transports, and shell process handling safe
under cancellation and concurrency.

Implementation tasks:

- [x] Replace scanner-based SSE parsing with a shared bounded reader-based SSE
      parser.
- [x] Use the shared SSE parser in provider streaming and MCP HTTP streaming.
- [x] Add explicit maximum SSE event size and clear oversized-event errors.
- [x] Add injectable `HTTPDoer` or `*http.Client` options for providers.
- [x] Add injectable and timeout-aware HTTP client options for MCP HTTP
      transports.
- [x] Replace per-call provider HTTP clients with a shared default client and
      tuned transport.
- [x] Make MCP request IDs concurrency-safe with `atomic.Int64` or a mutex.
- [x] Protect MCP HTTP `sessionID` reads and writes with synchronization.
- [x] Redesign MCP stdio transport around a read loop and response
      demultiplexer.
- [x] Ensure canceled MCP stdio requests cannot block forever in
      `json.Decoder.Decode`.
- [x] Change `BashTool.Execute` so timeout and cancellation wait for process
      cleanup before returning.
- [x] Replace fixed Unix process-kill sleep with a bounded grace-period wait.

Testing tasks:

- [x] Add SSE tests for multi-line events, CRLF input, large valid events, and
      oversized events.
- [x] Add fake provider tests that verify custom HTTP clients are used.
- [x] Add MCP HTTP race tests for concurrent tool calls.
- [x] Add MCP stdio cancellation tests with a blocking fake server.
- [x] Add Bash timeout and cancellation tests that verify `cmd.Wait` is joined.
- [ ] Run `go test -race ./tools/mcp ./providers ./tools`.
      Blocked locally on 2026-06-06: race builds require CGO and `gcc` is not
      installed in this Windows environment. Concurrency coverage and
      `go test ./...` pass.

Acceptance criteria:

- [x] Valid large SSE events no longer fail at the `bufio.Scanner` token limit.
- [x] Provider and MCP HTTP behavior can be configured by production callers.
- [x] Concurrent MCP tool calls do not race request IDs or session headers.
- [x] Bash cancellation does not leave SDK-owned wait goroutines running.

## Phase 12: Context-Aware Persistence And Scalable Stores

Goal: make session persistence obey caller deadlines and scale task updates
with patch size instead of session size.

Implementation tasks:

- [x] Add `context.Context` to the `core.SessionStore` interface.
- [x] Update in-memory and SQLite stores to implement the context-aware
      interface.
- [x] Thread session and run contexts through `Agent.Session`, `Session.append`,
      compaction skill replay, and task tools.
- [x] Replace SQLite `context.Background()` transactions and queries with
      `BeginTx`, `ExecContext`, `QueryContext`, and `QueryRowContext`.
- [x] Add compatibility adapters for existing custom stores if needed.
- [x] Deep-copy mutable records, messages, task metadata, content-block inputs,
      and provider metadata at in-memory store boundaries.
- [x] Replace SQLite full task-graph replacement with targeted task-row and
      task-edge mutations.
- [x] Implement scalable cycle validation for task dependency changes.

Testing tasks:

- [x] Add cancellation tests for slow or locked SQLite operations.
- [x] Add in-memory store mutation-isolation tests.
- [x] Add SQLite task update tests that verify only targeted rows and edges
      change.
- [x] Add SQLite task benchmarks for 100, 1,000, and 10,000 tasks.
- [x] Run `go test ./sessions/... ./tools/...` and relevant integration tests.
      Verified on 2026-06-07 with `go test ./sessions/... ./tools/...`,
      `go test ./...`, and
      `go test ./sessions/sqlite -run '^$' -bench 'BenchmarkStoreTask' -benchtime=1x`.

Acceptance criteria:

- [x] Store operations can be canceled by callers.
- [x] In-memory store callers cannot mutate stored state through returned maps
      or slices.
- [x] Updating one SQLite task no longer rewrites every task edge.
- [x] Session persistence remains backward compatible for existing stored data.

## Phase 13: Runtime Ownership, Concurrency Contracts, And Security Policy

Goal: clarify ownership boundaries and reduce unsafe shared mutable state.

Implementation tasks:

- [ ] Clone caller-provided tool registries in `NewAgent`.
- [ ] Stop lazy MCP, Skill, and Subagent registration from mutating
      caller-owned registries.
- [ ] Reduce `Agent.Session` lock scope so slow MCP connections and filesystem
      loads do not block unrelated session creation.
- [ ] Replace broad runtime loading locks with explicit once/state guards.
- [ ] Add a documented provider concurrency contract.
- [ ] Add a provider factory or clone hook for custom providers that are not
      safe for concurrent streams.
- [ ] Add Go doc comments describing concurrency guarantees for `Agent`,
      `Session`, `Provider`, `Tool`, `Registry`, and `SessionStore`.
- [ ] Add configurable filesystem root policy for read, write, edit, glob, and
      grep tools.
- [ ] Define symlink handling and absolute-path behavior for filesystem tools.

Testing tasks:

- [ ] Add tests proving external registries are unchanged after agent runtime
      loading.
- [ ] Add concurrent session creation tests with slow MCP and skill loaders.
- [ ] Add race tests for parent and subagent provider use.
- [ ] Add filesystem policy tests for allowed roots, denied roots, absolute
      paths, and symlink cases.
- [ ] Run `go test -race ./...` for affected packages.

Acceptance criteria:

- [ ] Agent runtime loading cannot mutate a registry still owned by the caller.
- [ ] Multiple sessions can be created without serializing on slow runtime
      resource loading.
- [ ] Provider concurrency expectations are explicit and tested.
- [ ] Embedded/server users can restrict filesystem tools to approved roots.

## Phase 14: Performance Hot Paths And Memory Efficiency

Goal: reduce avoidable CPU and memory costs in long sessions and large
workspaces.

Implementation tasks:

- [ ] Add benchmark fixtures for long message histories, large tool registries,
      large workspaces, and large task graphs.
- [ ] Cache stable system and tool token-estimate inputs.
- [ ] Track approximate message token deltas when appending messages.
- [ ] Recompute full token estimates only near compaction thresholds or after
      compaction.
- [ ] Stream grep fallback file scanning where multiline mode is not requested.
- [ ] Stop grep fallback rendering once `head_limit` and output caps are met.
- [ ] Use `strings.Builder` in hot provider translation paths that currently
      concatenate repeated strings.
- [ ] Use `slices` and `maps` package helpers where they simplify collection
      code without hiding deep-copy requirements.
- [ ] Consider `sync.Pool` only after benchmarks show repeated large temporary
      allocations.

Testing tasks:

- [ ] Add benchmarks for `estimateProviderTokens`.
- [ ] Add benchmarks for provider request translation.
- [ ] Add benchmarks for grep fallback on large trees.
- [ ] Add benchmarks for SQLite task updates.
- [ ] Add allocation assertions for hot-path benchmarks where stable.
- [ ] Run `go test -bench=. -benchmem` for targeted packages.

Acceptance criteria:

- [ ] Token estimation cost grows with new messages in normal turns instead of
      full history size.
- [ ] Grep fallback memory is bounded by current file and output limits for
      non-multiline searches.
- [ ] Performance refactors are backed by benchmark deltas.
- [ ] No hot-path optimization weakens behavior covered by parity tests.

## Phase 15: Error Handling, Observability, And Operational Hooks

Goal: make production failures diagnosable without parsing model-facing text.

Implementation tasks:

- [ ] Wrap store, MCP, provider, and config errors with operation context using
      `%w`.
- [ ] Preserve typed `*core.SkawldError` values through retry and stream
      boundaries.
- [ ] Use `errors.Join` for multi-close failures such as MCP client shutdown.
- [ ] Separate SDK-facing typed errors from model-facing tool result strings.
- [ ] Add optional structured logging through `*slog.Logger`.
- [ ] Add stable log fields for session ID, run ID, provider ID, tool name,
      attempt number, duration, retryability, and error kind.
- [ ] Add an observer hook for provider attempts, tool execution, permission
      callbacks, compaction, MCP calls, and store operations.
- [ ] Add duration tracking for permission callbacks without spawning unbounded
      goroutines.
- [ ] Document secret redaction rules for logs and observer payloads.

Testing tasks:

- [ ] Add `errors.Is` and `errors.As` tests for provider, store, MCP, and
      permission failures.
- [ ] Add tests for `errors.Join` behavior during MCP close.
- [ ] Add logger tests using a captured `slog.Handler`.
- [ ] Add observer callback tests for successful and failed runs.
- [ ] Add permission callback timeout/cancellation tests.

Acceptance criteria:

- [ ] Application code can classify important failures without string parsing.
- [ ] Logs and observer events provide enough context to debug failed runs.
- [ ] Observability hooks do not expose secrets or large raw payloads by
      default.
- [ ] Permission callback behavior is measurable and cancellation-safe.

## Phase 16: Package Boundaries And Maintainability

Goal: reduce duplicate parsing, weak internal typing, and package coupling.

Implementation tasks:

- [ ] Move shared SSE parsing to `internal/sse`.
- [ ] Move shared frontmatter parsing to `internal/frontmatter`.
- [ ] Replace duplicated skill and subagent frontmatter parsers with the shared
      parser.
- [ ] Move shared deep-copy helpers for JSON-like SDK values to an internal
      package.
- [ ] Add typed metadata structs for skill and subagent frontmatter.
- [ ] Add typed input parsers for built-in tools while preserving the generic
      `core.Tool` interface.
- [ ] Replace internal `map[string]interface{}` usage with typed structs where
      the schema is stable.
- [ ] Split config parsing from provider construction through a provider
      factory or binder.
- [ ] Add a package-structure document for the post-hardening layout.
- [ ] Normalize internal names around provider history, complete history,
      subagent directories, and runtime tools.

Testing tasks:

- [ ] Add shared frontmatter parser fixtures for skills and subagents.
- [ ] Add typed built-in tool input parser tests.
- [ ] Add config binder tests with fake providers.
- [ ] Add package import tests to prevent accidental dependency cycles.
- [ ] Run `go test ./...` and `go vet ./...`.

Acceptance criteria:

- [ ] Skill and subagent frontmatter behavior is consistent.
- [ ] Stable internal schemas are parsed once and used as typed values.
- [ ] Config parsing can be tested without importing concrete providers.
- [ ] Package boundaries remain clear for future SDK extensions.

## Phase 17: Production Validation And Release Gate

Goal: prove the hardened SDK is ready for long-running and high-concurrency
production use.

Implementation tasks:

- [ ] Add a production readiness checklist to release documentation.
- [ ] Add documented commands for race tests, leak-focused tests, benchmarks,
      and normal CI.
- [ ] Add stress-test fixtures for concurrent sessions, subagents, MCP calls,
      permission callbacks, and Bash cancellation.
- [ ] Add compatibility notes for any public API changes such as `RunHandle`,
      provider streams, context-aware stores, or filesystem policy.
- [ ] Update README and usage docs with the new lifecycle and concurrency
      contracts.
- [ ] Re-score production readiness after the hardening phases.

Testing tasks:

- [ ] Run `gofmt ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `go test ./...`.
- [ ] Run `go test -race ./...`.
- [ ] Run targeted benchmarks with `-benchmem`.
- [ ] Run examples build tests.

Acceptance criteria:

- [ ] No known goroutine leaks in lifecycle tests.
- [ ] No data races in the supported concurrency test suite.
- [ ] Benchmark results are recorded for the main hot paths.
- [ ] Documentation describes all production-relevant lifecycle, concurrency,
      security, and observability behavior.

## Production Hardening Sprint Grouping

Sprint 9:

- [x] Phase 10 run lifecycle cleanup
- [x] Phase 10 provider stream contract migration

Sprint 10:

- [x] Phase 11 SSE parser and HTTP client injection
- [x] Phase 11 MCP concurrency and cancellation fixes
- [x] Phase 11 Bash cleanup fixes

Sprint 11:

- [x] Phase 12 context-aware store interface
- [x] Phase 12 in-memory deep-copy boundaries
- [x] Phase 12 SQLite targeted task updates

Sprint 12:

- [ ] Phase 13 runtime ownership and registry isolation
- [ ] Phase 13 provider concurrency contract
- [ ] Phase 13 filesystem root policy

Sprint 13:

- [ ] Phase 14 benchmark baseline
- [ ] Phase 14 token estimate and grep fallback optimization
- [ ] Phase 15 typed errors and structured observability

Sprint 14:

- [ ] Phase 16 shared parsers and package boundary cleanup
- [ ] Phase 16 typed tool/config internals
- [ ] Phase 17 production validation and release gate
