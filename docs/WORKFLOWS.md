# Deterministic workflows

The `workflow` package is the provider-independent execution core for learned
or authored procedures. It does not ask a model to choose each known step.

A workflow has a stable ID and immutable numbered versions. Learned output is
saved as a candidate and must be published before execution. Values use typed
structural references:

```go
workflow.Value{Ref: "input.invoice.total"}
workflow.Value{Ref: "steps.lookup_po.output.total"}
```

Conditions use fixed operators (`eq`, `ne`, `gt`, `gte`, `lt`, `lte`,
`exists`, and `not_exists`). They are data, not executable source code.

Every tool call passes through:

```text
resolve typed arguments
        ↓
capability authorization
        ↓
risk and approval policy evaluation
        ↓
durable approval when required
        ↓
idempotency and retry checks
        ↓
tool runner
        ↓
runtime output-schema validation
        ↓
durable audit outbox
```

Unknown and non-idempotent side effects cannot be retried automatically.
High-risk actions require approval under the default risk policy. A pending
approval returns an `awaiting_approval` execution checkpoint; after a human
decides it, call `Executor.Resume`.

## Execution identity and authorization

Production execution requires an authenticated `core.Principal` with both a
tenant ID and actor ID. The same identity must already be attached to the
context with `core.WithPrincipal`; `Execute`, `Resume`, and `Recover` reject a
missing or different context identity. The runtime never silently promotes a
method argument into an authenticated context.

Tools declare exact required capabilities through
`core.ToolDescriptor.Permissions`. `policy.RolePolicy` maps trusted
application roles to capabilities and requires every declared capability
before delegating to its next evaluator:

```go
authorization, err := policy.NewRolePolicy(policy.RolePolicyOptions{
    RoleCapabilities: map[string][]string{
        "accountant": {"invoice.read", "payment.create"},
    },
})
```

Roles must come from the application's authenticated identity provider, not
from prompts, documents, model output, workflow input, or tool results. The
executor persists and authorizes the roles from the authenticated context,
never roles supplied only through the `Execute` method argument. The
default executor installs an empty role policy: tools with no declared
permissions retain the risk-policy behavior, while tools that declare
permissions fail closed until the application configures grants.

Approval persistence and approval authority are separate concerns. Production
applications should wrap an `ApprovalLifecycleStore` with
`policy.AuthorizedApprovalStore`. `ApprovalRolePolicy` obtains capabilities
from authenticated context roles and can prevent the requester from granting
their own request:

```go
approvalPolicy, _ := policy.NewApprovalRolePolicy(
    policy.ApprovalRolePolicyOptions{
        RoleCapabilities: map[string][]string{
            "finance-approver": {"approval.grant"},
        },
        RequireDistinctApprover: true,
    },
)
approvals, _ := policy.NewAuthorizedApprovalStore(
    workflowStorage.ApprovalLifecycle(), approvalPolicy,
)
```

New approval records persist `RequestedBy`. Grant, reject, and cancellation
capabilities are independently configurable. Deadline expiration remains a
deterministic store transition and does not pretend to be a human decision.

Before execution begins, the runtime validates task input and context against
the workflow's trusted structural contracts and checks every reference against
those contracts or a registered tool output schema. Invalid references fail
before the first tool call, preventing late validation after an earlier
real-world side effect.

The runtime also validates every actual tool result against the descriptor's
output schema before storing it in workflow state. An invalid read-only result
fails the step. An invalid result from a side-effecting operation moves the
execution to `recovery_required`, because the external action may already have
occurred and automatic replay could duplicate it.

Learned workflows also carry `ToolCatalogDigest`, derived from only the tools
they reference. `RegistryRunner` compares the current registry contracts with
that digest before reference validation or execution. Unrelated tool
registrations do not invalidate the workflow.

## Deterministic resolution

`workflow.Resolver` maps exact application task types to stable workflow IDs
and returns only the tenant-visible published version:

```go
resolver, err := workflow.NewResolver(workflow.ResolverOptions{
    Store: workflowStore,
    Routes: []workflow.Route{{
        TaskType: "invoice.reconcile",
        WorkflowID: "invoice-reconciliation",
    }},
})

version, err := resolver.Resolve(ctx, workflow.ResolutionRequest{
    TaskType: "invoice.reconcile",
})
```

Unknown task types fail with `not_found`; conflicting task type and workflow ID
fail with `conflict`. This resolver deliberately does not perform semantic or
LLM-based selection. Applications may propose a route agentically, but must
convert that proposal to an allowlisted exact task type before this boundary.

Static `Routes` are convenient for tests and fixed applications. Production
embeddings can configure `ResolverOptions.RouteStore` instead:

```go
routes := workflow.NewMemoryRouteStore() // or workflowStorage.Routes()
resolver, err := workflow.NewResolver(workflow.ResolverOptions{
    Store:      workflowStorage.Workflows(),
    RouteStore: routes,
})
```

`RouteStore` is tenant-scoped and uses optimistic revisions. Revision zero
creates a route; updating or deleting requires the latest revision. Stale
writers receive a typed conflict. The SQLite implementation persists route
mappings across restarts.

Prefer `automation.Lifecycle.SaveRoute` over writing the store directly. It
verifies that the target currently has a published workflow version and emits
a `workflow.route_changed` audit event. Resolution still checks publication at
execution time, so a stale or invalid route fails closed.

## Execution feedback

`workflow.ExecutionFeedback` records immutable semantic labels over terminal
executions:

- `accepted`;
- `correction`;
- `failure`;
- `unsafe`.

Feedback contains workflow/execution identity, terminal status, an optional
step ID, a machine-readable reason code, and—for corrections—a semantic action
name. It deliberately does not contain task inputs, tool arguments, tool
outputs, executable expressions, or replacement workflow steps.

Use `automation.Lifecycle.RecordFeedback` when the lifecycle has both
`Executions` and `Feedback` stores configured:

```go
feedback, err := lifecycle.RecordFeedback(
    ctx,
    execution.ID,
    workflow.FeedbackRequest{
        Disposition: workflow.FeedbackCorrection,
        StepID: "select_account",
        ReasonCode: "incorrect.account",
        CorrectedAction: "select_payable_account",
    },
    principal,
)
```

The method loads the immutable terminal checkpoint, derives tenant and workflow
identity from it, persists the label, and emits
`workflow.feedback_recorded`. Feedback never modifies, retrains, recompiles, or
publishes a workflow automatically. Applications must treat free-form comments
as access-controlled human content and redact secrets before storage.

`learning.AnalyzeFeedback` deterministically aggregates one tenant/workflow
version into disposition counts, reason-code patterns, and corrected-action
patterns. It deliberately excludes comments and only signals whether new
demonstrations are warranted; it never invents executable changes. Capture and
review new demonstrations before compiling a replacement candidate.

`automation.Lifecycle.ProposeImprovement` is the controlled bridge from that
feedback to a replacement candidate. It requires a concrete base version,
enough recorded feedback, and at least one new completed demonstration not
used by the base version. It compiles a candidate only. It never evaluates,
reviews, publishes, routes, or executes the result; those existing release
gates remain mandatory.

## Durable execution checkpoints

Configure `ExecutorOptions.Executions` to persist state before tool calls and
after every workflow transition:

```go
executor, err := workflow.NewExecutor(workflow.ExecutorOptions{
    Tools:      toolRunner,
    Policy:     authorization,
    Approvals:  workflowStorage.Approvals(),
    Audit:      workflowStorage.Audit(),
    AuditOutbox: workflowStorage.AuditOutbox(),
    Executions: workflowStorage.Executions(),
    ApprovalTTL: 24 * time.Hour,
})
```

`workflow.ExecutionStore` uses optimistic revisions. Create returns revision
one; every accepted update returns the next revision and stale writers receive
a typed conflict. Identity, input, context, completed steps, and terminal
executions are immutable.

Multiple workflow workers should configure a stable process-specific
`ExecutorOptions.WorkerID`, `ExecutionLeaseDuration`, and
`RequireExecutionLease`. `ExecutionLeaseStore` claims use monotonically
increasing fencing tokens. The executor renews its claim while running and
cancels its execution context if renewal fails. A stale worker cannot persist a
checkpoint after a newer worker takes ownership. SQLite and the memory test
store implement this contract.

`ExecutorOptions.ExecutionTimeout` establishes a workflow-wide deadline that is
persisted in every checkpoint. It bounds approval waiting as well as active
execution. `Executor.Cancel` and `automation.Lifecycle.CancelExecution`
explicitly cancel non-terminal executions, close a pending approval first, and
emit structured cancellation audit events.

The SQLite implementation stores approval checkpoints and terminal results
across process restarts. Load a checkpoint with `Executions().Get`, fetch the
exact workflow version recorded on it, and pass both to `Executor.Resume`.
Only `awaiting_approval` checkpoints can be resumed automatically.

Approval records may include `ExpiresAt`; `ExecutorOptions.ApprovalTTL`
populates it for new requests. Pending approvals that pass their deadline are
persistently expired during resume. `policy.ApprovalLifecycleStore` also
supports explicit listing, expiration, and cancellation, and
`automation.Lifecycle` exposes the corresponding controlled operations.

An execution in `recovery_required` means a side-effecting tool's outcome is
uncertain. Reconcile the idempotency key or external system state, then call
`Executor.Recover` (or `automation.Lifecycle.RecoverExecution`) with one
explicit decision:

- `confirmed_completed`, with schema-valid recovered output;
- `confirmed_not_executed`, which permits one explicit retry;
- `compensated`, which records terminal failure; or
- `canceled`, which records terminal cancellation.

Recovery requires the initiating authenticated actor and a bounded reason.
The runtime never guesses whether a real-world operation occurred.

Applications can configure `ExecutorOptions.Reconciler` with a
`workflow.ToolReconciler` or `ReconcilerRegistry`. `ReconcileRecovery` gives
trusted connector code the tool input, observed output, attempt count, and
idempotency key. The reconciler queries authoritative external state and
returns `completed`, `not_executed`, `compensated`, or `unknown` with a stable
evidence code. `unknown` leaves the execution recoverable. Model output,
documents, and other untrusted content must never implement this authority.

When `AuditOutbox` is configured, the executor durably enqueues every audit
event before attempting delivery to `Audit`. Sink failures leave events
pending without turning an already-committed business action into an apparent
failure. Use `audit.Dispatcher.Flush` from an application worker to retry
delivery. Downstream audit sinks must be idempotent by event ID.

For multiple production workers, use `audit.Worker` with an
`audit.LeasedOutbox`. Claims are tenant-scoped and lease-owned; failed
deliveries receive bounded exponential backoff and enter a dead-letter state
after `MaxAttempts`. Operators can inspect `DeadLetters` and explicitly
`Requeue` an event. SQLite persists leases, retry schedules, and dead letters
across restarts. Delivery remains at-least-once, so sinks must still be
idempotent by event ID.

`storage/sqlite` applies ordered, transactional migrations and records the
current version in `PRAGMA user_version`. It upgrades older databases on open,
rejects schema versions newer than the SDK, and persists pending audit outbox
deliveries across restarts.

Production databases should be opened with
`storage/sqlite.OpenWithOptions(..., RequireProtection: true)`.
`storage.AESGCMProtector` encrypts every JSON document with tenant-bound
AES-256-GCM authenticated encryption and supports external tenant key
providers. A database marked protected fails closed when reopened without its
protector. `AllowUnprotectedReads` is an explicit transitional option for
legacy plaintext rows; call `ProtectExistingDocuments` to encrypt all of the
authenticated tenant's existing rows transactionally, then reopen without the
fallback. Protected envelopes never fall back to plaintext or get
double-encrypted.

For key rotation, install a new current tenant key while keeping the previous
key readable, then call `Store.RotateProtectedDocuments`. The operation
decrypts and re-protects every tenant document in one transaction and fails on
plaintext or unrecognized rows. Verify reads before retiring the old key.

`Store.PurgeExpired` applies an explicit tenant-scoped
`storage.RetentionPolicy` transactionally. Zero duration retains a class
indefinitely. Purging never removes running executions, recording
demonstrations, pending approvals, or pending audit outbox events.

The `observation` package records semantic demonstrations. It intentionally
does not define browser, desktop, email, or API interception. Those belong in
adapters that emit normalized `observation.Event` values with explicit trust
labels.

Every new recorder-captured event receives a sensitivity classification.
Concrete adapters assign classification from trusted adapter configuration;
observed content cannot downgrade itself. Configure
`observation.NewRecorderWithOptions` with an `observation.Redactor` to
deterministically drop or mask exact semantic paths in event input, output,
context, decisions, results, initial context, and final results before they
reach persistence or learning.

The first concrete adapter is `observation/httpadapter`, which captures
HMAC-authenticated business events while preventing the request body from
choosing identity, source, or trust. See
[OBSERVATION_ADAPTERS.md](OBSERVATION_ADAPTERS.md).

The optional `learning.Compiler` accepts an application-supplied `Extractor`
(LLM-backed, rules-based, or human-authored). Extractor output is never
published or executed directly: the compiler overwrites identity/version
fields, verifies trace ownership, validates the workflow model, checks every
tool against a safety-aware catalog, and saves only a candidate.

`learning/structured` supplies the provider-neutral model-backed adapter. It
uses a single synthetic tool call as the structured-output contract, sends
salted fingerprints instead of raw observed values, rejects free text and
unknown JSON fields, allows only configured business tools and structural
references, and requires trusted event evidence for executable steps. See
[STRUCTURED_EXTRACTION.md](STRUCTURED_EXTRACTION.md).

## Learning from multiple demonstrations

`learning.Analyze` deterministically compares completed demonstrations before
an extractor is trusted to generalize them. It reports:

- semantic action support and relative position;
- exact sequence variants and pairwise sequence consistency;
- constant, variable, and optional field candidates without retaining the
  observed values;
- human corrections and errored actions;
- ambiguous transitions where the same observed state led to different next
  actions.

The analyzer identifies evidence; it does not invent conditions or executable
argument expressions.

When the same semantic action has multiple next-action outcomes, the analyzer
also reports `BranchCandidates`: redacted field paths whose observed
fingerprints perfectly separate those outcomes, with demonstration/event
evidence for each branch. Values and inferred predicates are intentionally
omitted. A structured extractor may use these as review evidence, but must
still propose a bounded workflow condition with cited events and pass compiler
validation and human review.

Use `Compiler.CompileMultiple` for multi-demonstration learning. The default
gate requires at least two demonstrations, 50% sequence consistency, no
ambiguous transitions, and evidence for every non-approval step. Extractors
receive the deterministic analysis and must return `workflow.EvidenceRef`
links for their proposed steps. The compiler verifies every demonstration and
event ID, computes learning metadata, and stores the output as a candidate
requiring human review.

When a prior version exists, `CompilationResult.Changes` reports added,
removed, modified, and reordered steps plus input-schema changes. Evidence-only
updates do not appear as executable behavior changes. This gives reviewers a
focused diff while retaining the complete evidence links on the candidate.

Legitimate conditional branches can have different sequences. Capture the
decision inputs or context that explains the branch. `AllowConflicts` exists
for explicit review workflows, but does not make a conflicting candidate safe
to publish.

Run the local, side-effect-free vertical example:

```sh
go run ./examples/invoice_reconciliation
go run ./examples/multiple_demonstrations
go run ./examples/workflow_evaluation
go run ./examples/agent_evaluation
go run ./examples/extractor_evaluation
go run ./examples/http_observation
go run ./examples/learned_invoice
```

The example records an invoice demonstration, publishes a reviewed workflow
version, compares fixture data deterministically, pauses before the simulated
accounting update, grants approval, resumes, and emits an audit trail.

The multiple-demonstration example compares three traces, detects a variable
invoice ID and an optional review path, validates step evidence, and saves a
candidate without publishing it.

The learned-invoice example is the complete credential-free vertical:
two semantic demonstrations, structured model output, evidence-validated
compilation, deterministic release evaluation, exact-digest human review,
tool-drift-checked publication, durable exact task resolution, an approval
checkpoint, idempotent execution, and terminal execution feedback.

Before publishing a replacement version, use the `evaluation` package to run
fixture-only regression scenarios and enforce reliability and safety gates.
See [EVALUATION.md](EVALUATION.md).

Production publication through `evaluation.Publisher` requires both a fresh
passing report for the exact candidate digest and the latest exact-digest
human review to be `approved`. Reviews are immutable, tenant-isolated records
available in memory and through `storage/sqlite`.

The `automation` package composes the application lifecycle: demonstration
recording, compilation, deterministic evaluation, human review, guarded
publication, and execution of the currently published version.

Use the agent and extractor runners separately for model-dependent behavior.
They preserve the distinction between deterministic workflow reliability and
agentic planning/extraction quality, while sharing the same release-gate
conventions.
