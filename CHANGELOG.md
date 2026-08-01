# Changelog

## Unreleased

- Closed production P1 trust-boundary gaps: compaction now shares provider
  protocol and budget enforcement with normal turns and contributes to total
  usage; permission callbacks and validators receive isolated tool-input
  copies; workflow coordinators atomically claim lease-ready work before
  applying batch limits; and SQLite commits workflow checkpoints with their
  audit-outbox events in one transaction.

- Added P2 production integrity controls: encrypted session envelopes are
  authenticated against tenant, actor, session, and payload purpose; production
  tools require valid trusted input/output schemas; post-policy input mutation
  is rejected; and provider usage accounting is non-negative, overflow-safe,
  cache-aware, and clamped to the remaining run budget.
- Added P1 production defense-in-depth: tenant-bound protected session
  storage, direct store actor isolation, dynamic tool-contract revalidation,
  strict provider-stream protocol and event budgets, per-turn output-token
  clamping, trusted output-schema requirements, and content-free audit and
  workflow worker health snapshots.
- Added fail-closed production constructors for agent and workflow runtimes,
  explicit durable-store marker contracts, strict session ownership, hard
  agent policy enforcement, runtime budgets, and agent tool-output validation.
- Added a leased durable workflow coordinator that resumes authoritative
  checkpoints and moves interrupted side-effecting tools to explicit recovery
  instead of replaying them.
- Removed predictable timestamp ID fallback, centralized build version
  reporting, enforced coverage/security analysis in CI, and added automated
  cross-platform releases with checksums, CycloneDX SBOMs, and GitHub
  provenance attestations.

## v0.2.0 - 2026-07-26

- Changed the Go module path to `github.com/ZekromNguyen/skawld-sdk-go`.
- Added fail-closed role/capability authorization, strict context-bound
  execution identities, runtime tool-output schema validation, durable audit
  outbox delivery, and explicit recovery for uncertain side effects.
- Added expiring and cancelable approvals, ordered transactional SQLite schema
  migrations, controlled feedback-to-candidate improvement proposals,
  evidence-supported branch-condition discovery, and a semantic browser
  observation adapter that rejects coordinate/selector replay data.
- Added capability-authorized approval decisions with requester/approver
  separation, leased audit delivery workers with backoff and dead letters,
  deterministic tool reconciliation for uncertain executions, tenant-bound
  AES-256-GCM SQLite document protection, and transactional retention purging.
- Added fenced workflow-execution leases with worker heartbeats, durable
  workflow deadlines and explicit cancellation, transactional document-key
  rotation, and trusted observation classification/redaction before storage.
- Added a provider-neutral structured-output workflow extractor with salted
  trace-value fingerprints, strict synthetic-tool decoding, trusted evidence
  and reference validation, bounded provider retries, model usage reporting,
  and compiler/evaluation integration.
- Added trusted workflow input/context contracts, trusted tool-output
  reference validation, execution preflight before side effects, immutable
  memory/SQLite workflow reviews, review-and-evaluation-gated publication,
  and an end-to-end automation lifecycle facade.
- Added registry-derived structured-extraction tool catalogs, learned-workflow
  tool contract fingerprints with compile/publish/execute drift checks, exact
  deterministic task-to-workflow resolution, production provider transport
  coverage, unsafe-candidate extraction gates, and a complete learned-invoice
  vertical example.
- Added tenant-scoped durable workflow routes with optimistic revisions,
  published-target lifecycle checks and audit events, plus immutable terminal
  execution feedback with bounded semantic correction labels and memory/SQLite
  stores and content-minimizing deterministic feedback analysis.
- Added optimistic, tenant-isolated workflow execution stores with immutable
  identity/input boundaries, monotonic state transitions, SQLite restart
  recovery, and executor checkpoints before tool calls and after every state
  transition.
- Added a transport-neutral semantic observation adapter contract and a
  strict, bounded HTTP business-event adapter with HMAC tenant/actor identity,
  configurable trust boundaries, timestamp validation, and atomic replay
  protection.
- Added provider-independent agent-runtime and workflow-extractor evaluation
  with safety, accuracy, token, and latency gates; bounded scenario
  concurrency; content-minimizing reports; and tenant-isolated in-memory and
  SQLite report stores.
- Added a vendor-neutral telemetry adapter with correlated operation
  counters, durations, error counters, completed spans, and a bounded
  concurrency-safe memory sink.
- Added deterministic workflow evaluation with fixture-only tools, fail-closed
  approvals, reliability/safety/cost/latency metrics, configurable release
  gates, guarded candidate publication, stricter default approval policy, and
  tenant-isolated in-memory and SQLite report stores.
- Added deterministic multi-demonstration analysis, including action support,
  sequence consistency, redacted parameter candidates, correction/error
  findings, ambiguous-transition detection, and evidence-validated candidate
  compilation with behavior-focused version diffs.
- Added a versioned deterministic workflow runtime with typed references,
  conditions, validation, policy/approval checkpoints, idempotency-aware
  retries, structured audit events, and semantic demonstration traces.
- Added durable SQLite stores for workflow versions, demonstrations, approvals,
  and audit events, including tenant isolation.
- Added tool risk/side-effect/idempotency metadata, explicit capability
  profiles, private-network protections, cwd-confined filesystem defaults,
  untrusted-content provenance, and agent-owned process/browser/cron cleanup.
- Fixed skill `allowed_tools` bypassing authorization, Raven permission dialogs
  not resolving runtime callbacks, non-success provider stop reasons being
  reported as success, immediate provider retries, and flaky process cleanup
  readiness.
- Added the fixture-only invoice reconciliation vertical example.
- Added SKILL.md loading, skill invocation events, one-turn skill overlays, and invoked-skill replay.
- Added MCP server configuration, lifecycle management, MCP-backed tools, and MCP examples.
- Added subagent definition loading, built-in default subagent, delegated child sessions, filtered child tool registries, and wrapped subagent events.
- Added JSON config loading for provider/model/runtime settings.
- Added compaction support for long-running sessions.
- Added provider hardening and fake HTTP/SSE integration coverage for Anthropic and OpenAI adapters.
