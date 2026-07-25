# Compatibility Notes

This document tracks public API changes made during the production hardening
phases (Sprints 9–14). Refer to this when upgrading from a pre-hardening SDK
version.

## Workflow operational safety P1

`workflow.Execution` now persists an optional `DeadlineAt` and supports the
additive `canceled` step state. Existing executions without a deadline remain
unbounded and compatible. `ExecutorOptions.ExecutionTimeout` is opt-in.

Distributed workers can opt into `workflow.ExecutionLeaseStore` with
`ExecutorOptions.WorkerID`. `RequireExecutionLease` fails construction when the
store cannot provide fenced claims. Existing single-process callers that omit a
worker ID retain optimistic-revision behavior.

The workflow SQLite schema is version 5. Migration 5 adds execution lease
owner, expiry, and fencing-token columns. Older databases migrate
transactionally on open; newer databases continue to fail closed.

Observation sensitivity is additive. Legacy events with an empty sensitivity
remain readable; recorder-created events default to `internal`, while concrete
adapters assign their configured trusted classification.

## Module path migration (2026-07-25)

The canonical module path is now:

```go
github.com/ZekromNguyen/skawld-sdk-go
```

Consumers must update imports from `github.com/skawld/skawld-sdk-go`. This is a
Go module identity change and should be released as a breaking version unless
the old repository publishes a forwarding compatibility module.

## Workflow production boundary hardening (2026-07-26)

Workflow execution now requires a complete `core.Principal` and the exact same
tenant/actor identity in the `context.Context`. Callers must use
`core.WithPrincipal`; `Execute`, `Resume`, and `Recover` no longer accept a
principal argument as implicit authentication.

The default workflow policy now includes an empty fail-closed
`policy.RolePolicy`. Existing tools that declare no
`ToolDescriptor.Permissions` keep their previous risk-policy behavior. Any
tool declaring permissions is denied until the application supplies trusted
role-to-capability grants.

Actual tool outputs are now validated against `OutputSchema`. Output schemas
that previously described references only are therefore enforced at runtime.
Side-effecting tools whose result cannot be trusted enter the new additive
`recovery_required` execution/step status instead of being retried.

Approvals gained additive `ExpiresAt` and `canceled` lifecycle support.
Third-party minimal `ApprovalStore` implementations remain source compatible;
listing, expiration, and cancellation use the optional
`ApprovalLifecycleStore` extension. Automatic expiration requires that
extension.

`storage/sqlite.Open` now applies transactional migrations tracked with
`PRAGMA user_version`. Schema version 2 adds the durable audit outbox. Opening
a database created by a newer SDK fails closed rather than attempting a
downgrade.

`ExecutorOptions.AuditOutbox` and `ApprovalTTL` are additive. When an outbox is
configured, audit sink delivery becomes asynchronous in failure semantics:
enqueue failure still fails the operation, while downstream sink failure
leaves a pending durable event for `audit.Dispatcher.Flush`.

## Workflow operational safety extensions (2026-07-26)

`policy.Approval` gained the additive `RequestedBy` field. New memory and
SQLite approval requests bind it to the authenticated context actor.
`AuthorizedApprovalStore` is an additive decorator; existing bare stores
remain persistence adapters, but production applications should use the
decorator to enforce decision capabilities and separation of duties.

`audit.LeasedOutbox` extends `audit.Outbox` with claim, acknowledge, failure,
dead-letter listing, and requeue operations. `storage/sqlite.Store` exposes it
through `LeasedAuditOutbox`. Existing `AuditOutbox` and `Dispatcher.Flush`
callers remain source compatible.

`ExecutorOptions.Reconciler`, `Executor.ReconcileRecovery`,
`automation.Lifecycle.ReconcileExecution`, and
`RecoveryRequest.EvidenceCode` are additive. Reconciliation adapters must
query authoritative external systems and must not treat model output as
evidence.

The workflow SQLite schema version is now 4. Version 3 adds outbox lease,
backoff, and dead-letter columns; version 4 adds retention timestamps and the
document-protection marker.

`sqlite.Open` retains its local-development plaintext behavior.
`sqlite.OpenWithOptions` adds document protection and production fail-closed
configuration. Once a database contains the protection marker, opening it
without the configured protector is rejected. Applications adopting
protection for an existing plaintext database must explicitly enable
`AllowUnprotectedReads`, call `ProtectExistingDocuments`, then reopen without
the fallback during a controlled migration; it is not enabled implicitly.

## Learned workflow tool catalog verification (2026-07-26)

`learning.Compiler` now requires its `Tools` dependency to implement
`workflow.ToolCatalogFingerprinter` whenever an extracted candidate contains a
tool step. Use `structured.NewRegistryCatalog` with an explicit tool-name
allowlist for model-backed learning.

Learned candidates now carry `workflow.Version.ToolCatalogDigest`.
`evaluation.Publisher` must receive `PublisherOptions.ToolCatalog` to publish
such a candidate, and the runtime tool runner must implement the same
fingerprinting extension to execute it. `workflow.RegistryRunner` implements
the extension. Human-authored workflows without a digest remain compatible.

## Durable routes and execution feedback (2026-07-26)

`workflow.Route` gained additive persistence metadata fields. Existing static
route literals using only `TaskType` and `WorkflowID` remain compatible.
Applications may switch `workflow.Resolver` from `ResolverOptions.Routes` to
`ResolverOptions.RouteStore`; configuring both is rejected to keep resolution
unambiguous.

`automation.Options.Routes`, `Executions`, and `Feedback` are optional unless
calling the corresponding route or feedback lifecycle methods. Existing
lifecycle construction remains compatible. Opening `storage/sqlite.Store`
creates `workflow_routes` and `workflow_feedback` tables automatically.

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

As of the workflow foundation release, the zero value is cwd-confined and
rejects symlink escapes. Applications that require the historical unrestricted
behavior must opt in explicitly with
`tools.FilesystemPolicy{Unrestricted: true}`.

## Phase 16: Config ProviderFactory

`config.File.AgentOptions(ctx)` still works but delegates to
`AgentOptionsWithFactory(ctx, factory)`. Use `LoadAgentOptionsWithFactory`
to inject a custom factory for testing without concrete provider imports.
