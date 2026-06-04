# Migration TODO

This list tracks the remaining work needed to reach feature parity with the
original TypeScript `@skawld/agent-sdk`.

Status reviewed against the current Go codebase and tests on 2026-06-04.

For phased Scrum-style delivery planning, see [`SCRUM_PLAN.md`](./SCRUM_PLAN.md).

## Core Engine

- [ ] Add true compaction support:
  - [ ] `CompactionStrategy` interface
  - [ ] default keep-last-10-turns summarizer
  - [ ] forced compaction after context-length provider errors
  - [ ] `compaction` event emission
- [x] Improve scheduler parity:
  - [x] adjacent-batch partitioning by `parallelSafe`
  - [x] bounded concurrent read-tool execution
  - [x] synthetic `tool_call_end` events on abort
  - [x] event interleaving from tools that emit nested events
- [x] Add run abort API equivalent to `Session.abort()`.
- [ ] Add one active run cleanup semantics for abandoned iterators.
- [x] Preserve provider metadata emitted by providers across session
      append/resume.
- [x] Add full partial-assistant coverage for text, thinking, and tool JSON deltas.
- [x] Add `maxRetries` retry loop parity around provider streams.
- [ ] Add context window based proactive compaction threshold logic.

## Providers

- [x] Ship baseline streaming adapters for Anthropic Messages, OpenAI Chat
      Completions, and OpenAI Responses.
- [ ] Harden Anthropic adapter:
  - [ ] exact Messages API wire translation for all content block types
  - [ ] prompt caching breakpoints and TTL support
  - [ ] thinking and effort support
  - [ ] complete usage mapping including cache read/write tokens
  - [ ] Anthropic error mapping to typed Skawld errors
- [ ] Harden OpenAI Chat Completions adapter:
  - [ ] image block translation
  - [ ] tool-result image fallback behavior
  - [x] OpenAI-compatible base URL, default header, and context-window options
  - [ ] typed error mapping and retry-after parsing
- [ ] Harden OpenAI Responses adapter:
  - [x] basic text/function-call SSE streaming
  - [ ] response metadata capture for `response_id` and `output_items`
  - [ ] previous response id support
  - [ ] reasoning summary and encrypted reasoning support
  - [ ] output item metadata replay
  - [ ] complete incomplete/refusal/max-token stop reason mapping
- [ ] Add integration tests using fake HTTP/SSE servers.

## Tools

- [x] Bring filesystem tools to TypeScript parity:
  - [x] streaming large-file reads
  - [x] per-line truncation semantics
  - [x] device path guards on Unix-like systems
  - [x] better binary detection and media handling
  - [x] CRLF-preserving edit replacements
- [x] Improve `Glob` parity:
  - [x] full `**` glob support
  - [x] dotfile behavior matching TypeScript
  - [x] ripgrep-backed fast path when available
  - [x] exact mtime sorting behavior
- [x] Improve `Grep` parity:
  - [x] ripgrep-backed fast path
  - [x] multiline support
  - [x] `type`, `-A`, `-B`, `-C`, and complete output modes
  - [x] fallback implementation equivalence tests
- [x] Improve `Bash` parity:
  - [x] process-tree termination
  - [x] stdout/stderr streaming caps matching TypeScript
  - [x] Windows process handling and hidden window behavior
  - [x] abort behavior matching scheduler expectations
- [x] Complete task tools:
  - [x] basic `TaskCreate`, `TaskList`, `TaskGet`, and `TaskUpdate` tools
  - [x] in-memory task CRUD storage
  - [x] dependency edge add/remove support
  - [x] cycle detection
  - [x] metadata null-delete semantics
  - [x] deleted status compatibility

## Sessions

- [x] Implement in-memory session store:
  - [x] session records
  - [x] stored messages with monotonic sequence
  - [x] task persistence for the current process
  - [x] invoked-skill record storage API
- [ ] Implement SQLite session store:
  - [ ] session records
  - [ ] stored messages with monotonic sequence
  - [ ] task persistence
  - [ ] task dependency edges
  - [ ] invoked-skill persistence
  - [ ] close/reopen behavior
- [ ] Add persistence/resume tests matching TypeScript behavior.
- [ ] Add migration-safe schema initialization.

## Permissions

- [ ] Match TypeScript rule semantics exactly:
  - [x] default, `acceptEdits`, and `yolo` mode defaults
  - [x] basic tool rules
  - [x] basic path rules
  - [x] bash command rules
  - [x] named tool argument rules
  - [ ] complete tool argument rules
  - [ ] rule ordering and precedence
- [x] Add input rewriting tests for `CanUseTool`.
- [x] Add validation for invalid permission callback responses.
- [x] Emit `permission_request` events before callback execution.
- [x] Add permission-request event batching before callback execution.

## MCP

- [ ] Implement MCP server config types:
  - [ ] stdio server config
  - [ ] HTTP server config
- [ ] Implement MCP client lifecycle:
  - [ ] connect on first `Agent.Session`
  - [ ] register MCP tools as `mcp__server__tool`
  - [ ] close child/server connections on `Agent.Close`
  - [ ] retry connection after failed first attempt
- [ ] Implement MCP tool result conversion.
- [ ] Implement MCP naming helpers and collision handling.
- [ ] Add MCP end-to-end test with an echo server.

## Skills

- [ ] Implement `.skawld/skills/<name>/SKILL.md` loader.
- [ ] Parse frontmatter fields:
  - [ ] `name`
  - [ ] `description`
  - [ ] `when_to_use`
  - [ ] `argument_hint`
  - [ ] `allowed_tools`
  - [ ] `model`
- [ ] Implement skill listing prompt block.
- [ ] Implement Skill tool.
- [ ] Implement skill argument substitution.
- [ ] Implement one-turn skill overlays:
  - [ ] additive allowed tools
  - [ ] model override
- [ ] Persist invoked skills for resume/compaction replay.
- [ ] Emit `skills_loaded`, `skill_invoked`, and `skill_completed` events.

## Subagents

- [ ] Implement `.skawld/agents/<name>.md` loader.
- [ ] Implement built-in default subagent.
- [ ] Implement subagent registry.
- [ ] Implement Subagent tool.
- [ ] Implement child session runner with filtered tool registry.
- [ ] Wrap child events as parent `subagent_event`.
- [ ] Support nested subagent events.
- [ ] Add subagent end-to-end tests.

## Configuration

- [ ] Port config schema types.
- [ ] Add config loader equivalent to TypeScript `src/config`.
- [ ] Decide Go-specific config file format and precedence.
- [ ] Add tests for missing/invalid config.

## Documentation And Examples

- [ ] Add examples equivalent to TypeScript:
  - [x] minimal agent
  - [ ] MCP agent
  - [ ] interactive CLI
- [ ] Document provider setup and environment variables.
- [ ] Document custom tools in Go.
- [ ] Document permission callbacks in Go.
- [ ] Document session persistence once SQLite lands.

## Test Parity

- [x] Port core loop tests.
- [x] Port scheduler tests.
- [ ] Port provider translation tests.
- [ ] Port permissions tests:
  - [x] permission modes
  - [x] basic tool, path, bash, and named-argument rules
  - [x] callback validation and input rewriting
  - [x] permission-request batching
  - [ ] full TypeScript parity fixture coverage
- [ ] Port tool tests:
  - [x] filesystem tools
  - [x] `Glob`
  - [x] `Grep`
  - [x] `Bash`
  - [x] task tools
- [ ] Port session store tests:
  - [x] in-memory task storage behavior
  - [ ] full persistence/resume parity
- [ ] Port MCP tests.
- [ ] Port skills and subagent tests.
- [x] Add public API/surface test.
- [x] Add examples build test.
