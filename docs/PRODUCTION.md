# Production Runtime

The default constructors remain optimized for local development. Production
embeddings must opt in to the fail-closed constructors.

## Agent boundary

Use `skawld.NewProductionAgent` with:

- an authenticated tenant and actor;
- an explicit read-only tool registry whose tools declare finite timeouts and
  at least one required capability and trusted input/output schemas;
- a `core.ProtectedSessionStore`;
- a hard `policy.Evaluator`;
- a durable audit outbox;
- finite run, total/per-turn token, tool-call, provider-event,
  provider-response, tool-result, and session limits.

The production agent rejects `yolo`, unscoped sessions, tools without timeouts,
and side-effecting tools. Business mutations must execute as reviewed,
published workflows so approval, idempotency, checkpoints, and uncertain
outcome recovery cannot be bypassed.

Production mode disables repository-local skills by default and always
disables subagents. Set `ProductionOptions.AllowSkills` only when the skill
directory is a reviewed, deployment-controlled instruction source. MCP tools
cannot execute directly through the production agent; expose them through the
deterministic workflow runtime with explicit risk and recovery metadata.
Tools are revalidated after lazy runtime loading and again before execution so
a mutable or dynamically discovered descriptor cannot bypass construction
checks. Normalized tool inputs are validated against the advertised schema
before policy evaluation and immediately before execution. A production
permission or approval callback cannot rewrite an invocation after the hard
policy authorized it; callbacks and validators receive isolated deep copies,
and execution uses the immutable authorized snapshot.

Provider streams are treated as untrusted protocol input. Production validates
tool-call lifecycle ordering and JSON arguments, rejects unsupported events,
bounds event count and encoded response size, requires `message_end`, and
clamps every request to `MaxOutputTokensPerTurn` and the remaining cumulative
run allowance. Usage values must be non-negative and non-overflowing. Total
usage includes input, output, cache-read, and cache-creation tokens and is
validated before the assistant response is persisted. Provider calls made by
conversation compaction use the same protocol, byte, event, output-token, and
cumulative usage boundaries; compaction usage is included in the run total.

Interactive permissions are an additional user-experience control. They cannot
override a hard policy denial.

## Workflow boundary

Use `workflow.NewProductionExecutor`. It requires:

- an explicit capability/risk policy;
- durable, protected approval, execution, and audit-outbox stores;
- an execution store that commits checkpoints and audit-outbox events in the
  same transaction as the configured outbox;
- a deterministic side-effect reconciler;
- execution leases and a stable worker ID;
- finite approval and workflow deadlines;
- maximum step, tool-output, and checkpoint sizes.

Use `workflow.Coordinator` to atomically claim bounded, lease-ready
non-terminal work and resume from the authoritative stored checkpoint. Ready
rows are selected before the batch limit is applied, so live leases cannot
starve older recoverable work. The coordinator requires a service principal
carrying `workflow.worker` by default.
An interrupted side-effecting tool is moved to `recovery_required`; it is never
blindly replayed.

## Identity migration

Production mode refuses legacy sessions without both reserved owner fields:

- `_skawld_tenant_id`
- `_skawld_actor_id`

Before enabling production mode, assign trusted ownership to existing records
or quarantine/delete records whose owner cannot be established. Never derive
ownership from model output, message content, or workflow inputs.
`sessions.MigrateLegacySessionIdentity` performs the conflict-checked update
for a session whose trusted owner is known.

## Storage

The built-in workflow SQLite adapter is durable and supports fail-closed
tenant-bound document protection. Open production workflow storage with
`storage/sqlite.OpenWithOptions`, `RequireProtection: true`, and a
deployment-backed key provider.

Pass `store.Executions()` and `store.AuditOutbox()` from the same SQLite
`Store` instance to `NewProductionExecutor`. The constructor verifies that
both adapters share an atomic transaction domain. Mixing execution and outbox
adapters from different database instances is rejected.

Session content needs the same protection. Wrap a durable session adapter with
`sessions.NewProtectedStore`. The decorator keeps only tenant/actor ownership,
record identifiers, timestamps, task status, and dependency edges visible.
Private metadata, messages, invoked skills, and task content are encrypted
with the configured tenant-bound `storage.DocumentProtector`. Direct list,
load, mutation, and deletion operations enforce exact tenant and actor
ownership. The encrypted envelope also authenticates the tenant, actor, session
ID, and payload purpose, preventing a protected value from being substituted
into another actor, session, or record kind.

`ProtectedStore` intentionally refuses legacy plaintext records. Migrate
existing session data through a controlled export/decrypt/re-import process
into a fresh protected store before enabling `NewProductionAgent`; it never
silently treats old plaintext as trusted production data.

The durable and protected marker interfaces in `core`, `policy`, `workflow`,
and `audit` prevent production constructors from accidentally using in-memory
or plaintext stores. Transitional workflow stores opened with
`AllowUnprotectedReads` do not claim protection and are rejected until their
legacy rows have been migrated and the fallback is disabled.

## Worker readiness

`audit.Worker.Health` and `workflow.Coordinator.Health` return content-free
operational snapshots with last attempt/success timestamps, consecutive
failure counts, worker identity, and the last bounded result. They are safe to
adapt into application readiness endpoints because they exclude payloads,
tenant data, and raw errors. A worker is ready only after a successful poll;
any failed poll clears readiness until a later success. Use the snapshot's
`Healthy(now, maxStaleness)` method so a worker that stopped polling eventually
becomes unhealthy.

## Release evidence

CI enforces race tests, cross-builds, static analysis, reachable vulnerability
scanning, and a total coverage floor. Tags matching `v*` build Raven for Linux,
macOS, and Windows on amd64 and arm64, generate checksums and a CycloneDX SBOM,
create GitHub provenance attestations, and publish a GitHub Release.

macOS notarization and Windows Authenticode signing still require project-owned
signing identities and must be configured before distributing Raven to
end-users outside controlled environments.
