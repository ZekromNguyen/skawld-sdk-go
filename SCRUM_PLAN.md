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
- [ ] Add cleanup semantics for abandoned run iterators.
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
- [ ] Harden Anthropic Messages translation for all content block types.
- [ ] Add Anthropic prompt caching breakpoints and TTL support.
- [ ] Add Anthropic thinking and effort support.
- [ ] Add Anthropic usage and typed error mapping.
- [ ] Harden OpenAI Chat image and tool-result translation.
- [x] Add OpenAI-compatible base URL, default header, and context-window options.
- [ ] Harden OpenAI Responses previous-response replay.
- [ ] Capture OpenAI Responses response metadata for replay.
- [ ] Add OpenAI Responses reasoning summary and encrypted reasoning support.
- [ ] Complete stop reason and retry-after mapping.

Testing tasks:

- [x] Port provider translation tests.
- [ ] Add fake HTTP/SSE server tests for Anthropic.
- [ ] Add fake HTTP/SSE server tests for OpenAI Chat.
- [ ] Add fake HTTP/SSE server tests for OpenAI Responses.
- [ ] Add provider error mapping tests.
- [x] Add provider retry-loop tests with fake providers.
- [ ] Add provider adapter retry-after tests.

Acceptance criteria:

- [ ] Provider requests match the expected wire shape.
- [ ] Streaming wire events map into the normalized provider event contract.
- [ ] Typed provider errors are stable enough for application code.

## Phase 5: Compaction

Goal: support long-running sessions without losing important context.

Implementation tasks:

- [ ] Add `CompactionStrategy` interface.
- [ ] Implement default keep-last-10-turns compaction.
- [ ] Add summarization provider call.
- [ ] Add proactive context-window threshold logic.
- [ ] Add forced compaction after context-length errors.
- [ ] Re-inject skill listing and invoked skill bodies after compaction.
- [ ] Emit `compaction` events with before/after counts.

Testing tasks:

- [ ] Port compaction unit tests.
- [ ] Add strategy no-op suppression tests.
- [ ] Add forced compaction retry tests.
- [ ] Add provider-view versus full-history tests.
- [ ] Add skill replay after compaction tests once skills are available.

Acceptance criteria:

- [ ] Compaction changes only provider view, not full stored history.
- [ ] Context-length failures get one forced compaction retry.
- [ ] Consumers receive accurate compaction events.

## Phase 6: MCP Integration

Goal: expose external MCP server tools through the Go SDK.

Implementation tasks:

- [ ] Implement MCP server config types for stdio and HTTP.
- [ ] Implement MCP client lifecycle.
- [ ] Connect MCP servers lazily on first `Agent.Session`.
- [ ] Register tools as `mcp__server__tool`.
- [ ] Add MCP tool result conversion.
- [ ] Add naming and collision handling.
- [ ] Close MCP resources on `Agent.Close`.

Testing tasks:

- [ ] Add MCP unit tests for config and naming.
- [ ] Add MCP result conversion tests.
- [ ] Add MCP lifecycle tests.
- [ ] Add end-to-end echo MCP server test.

Acceptance criteria:

- [ ] MCP tools appear in the system event and provider tool schemas.
- [ ] Failed first connection can be retried later.
- [ ] MCP resources close cleanly.

## Phase 7: Skills

Goal: port SKILL.md loading and one-turn skill overlays.

Implementation tasks:

- [ ] Load `.skawld/skills/<name>/SKILL.md`.
- [ ] Parse frontmatter fields.
- [ ] Build skill listing prompt block.
- [ ] Implement Skill tool.
- [ ] Implement skill argument substitution.
- [ ] Implement additive allowed-tools overlay.
- [ ] Implement model override overlay.
- [ ] Persist invoked skills.
- [ ] Emit skills events.

Testing tasks:

- [ ] Port skill loader tests.
- [ ] Port listing tests.
- [ ] Port substitution and shell-split tests.
- [ ] Add Skill tool execution tests.
- [ ] Add resume and compaction replay tests.

Acceptance criteria:

- [ ] Skills load lazily on first session.
- [ ] Informational skills are auto-allowed.
- [ ] Skill overlays affect exactly one assistant turn.

## Phase 8: Subagents

Goal: support nested agent runs and hierarchical event streams.

Implementation tasks:

- [ ] Load `.skawld/agents/<name>.md` definitions.
- [ ] Implement built-in default subagent.
- [ ] Implement subagent registry.
- [ ] Implement Subagent tool.
- [ ] Implement child session runner.
- [ ] Filter child tool registry by agent definition.
- [ ] Wrap child events as `subagent_event`.
- [ ] Support nested subagent events.

Testing tasks:

- [ ] Port subagent loader tests.
- [ ] Port registry tests.
- [ ] Port runner tests.
- [ ] Add nested subagent event tests.
- [ ] Add subagent end-to-end tests.

Acceptance criteria:

- [ ] Parent run streams child events without breaking event ordering.
- [ ] Child sessions inherit the correct run options.
- [ ] Nested subagents are observable by UI consumers.

## Phase 9: Configuration, Documentation, And Release Readiness

Goal: make the Go SDK consumable by application developers.

Implementation tasks:

- [ ] Port config schema types.
- [ ] Implement Go config loader and precedence rules.
- [x] Add minimal example.
- [ ] Add MCP example.
- [ ] Add interactive CLI example.
- [ ] Document provider setup and environment variables.
- [ ] Document custom tools.
- [ ] Document permission callbacks.
- [ ] Document SQLite session persistence.
- [ ] Add changelog and release checklist.

Testing tasks:

- [ ] Add config loader tests.
- [ ] Add missing/invalid config tests.
- [x] Add examples build tests.
- [x] Add public API/surface tests.
- [ ] Run full parity suite before release.

Acceptance criteria:

- [x] Developers can follow docs to run a minimal agent.
- [x] Examples compile in CI.
- [ ] Release checklist clearly states remaining known gaps.

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

- [ ] Phase 4 provider hardening
- [ ] Phase 5 compaction

Sprint 6:

- [ ] Phase 6 MCP

Sprint 7:

- [ ] Phase 7 skills

Sprint 8:

- [ ] Phase 8 subagents
- [ ] Phase 9 docs and release readiness
