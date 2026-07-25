# Structured workflow extraction

`learning/structured` is the production-oriented, provider-neutral adapter for
turning completed semantic demonstrations into a review-only
`workflow.Version` candidate.

It implements both `learning.Extractor` and `learning.DetailedExtractor`.
`DetailedExtractor` reports model identity, provider-call count, and token
usage to the evaluation release gates.

## Security boundary

The adapter does not send raw observed values to a model. For each extraction
request it creates a random salt and projects scalar values to type plus a
salted fingerprint. Equal values within the request retain equal fingerprints,
which allows dependency inference without retaining invoice IDs, emails, or
other observed values. Intent text, errors, entity IDs, and timestamps are not
included.

The provider receives:

- a fixed system policy;
- a trusted, explicitly configured business-tool catalog;
- a redacted demonstration projection marked `untrusted_content`; and
- one synthetic `submit_workflow_candidate` tool.

The adapter rejects free-text answers, multiple or unknown tool calls, unknown
JSON fields, malformed JSON, unsupported references, unregistered business
tools, invented literals by default, and executable steps without trusted
evidence. Model output never publishes or executes a workflow. Pass the
extractor to `learning.Compiler`, which rebinds identity and version, verifies
tool safety metadata and evidence, and saves a candidate for review.

## Construction

```go
catalog, err := structured.NewRegistryCatalog(structured.CatalogOptions{
    Registry: registry,
    Names: []string{"erp.lookup_invoice"},
    TrustedDescriptions: map[string]bool{
        "erp.lookup_invoice": true,
    },
})
if err != nil {
    return err
}

extractor, err := structured.New(structured.Options{
    Provider: provider,
    Model:    "provider-model-id",
    Catalog:  catalog,
    Observer: observer,
})
if err != nil {
    return err
}

compiler := learning.Compiler{
    Extractor: extractor,
    Tools:     catalog,
    Store:     workflowStore,
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "invoice": map[string]interface{}{"type": "object"},
        },
    },
    ContextSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "account_id": map[string]interface{}{"type": "string"},
        },
    },
}
```

Input and context schemas are trusted application contracts. They are copied
onto the candidate by the compiler; the model cannot create or broaden them.
Every `input.*` and `context.*` reference is checked against these contracts.
Every `steps.<id>.output.*` reference is checked against the referenced
business tool's trusted output schema. The compiler repeats reference
validation for non-model extractors, and the deterministic executor performs
the same preflight before any tool can create a side effect.

`RegistryCatalog` is the preferred construction path. Its required `Names`
allowlist prevents accidentally exposing the entire application registry, and
it derives input schemas, output schemas, and safety metadata from the same
registered tools used at runtime. Descriptions are omitted from the model
prompt unless the matching `TrustedDescriptions` entry is true. JSON Schema
annotation prose is removed even from trusted tool schemas.

The compiler records a digest of every referenced tool contract. Guarded
publication verifies that digest against the current catalog, and
`workflow.RegistryRunner` verifies it again before execution. A schema,
description, risk, side-effect, idempotency, timeout, permission, network,
secret-handling, untrusted-output, or parallel-safety change therefore fails
closed until the workflow is recompiled, evaluated, reviewed, and published.
The lower-level `Tools []ToolDefinition` option remains available for adapters,
but production compiler catalogs must implement
`workflow.ToolCatalogFingerprinter`.

Call compilation with an authenticated tenant:

```go
ctx := core.WithPrincipal(ctx, core.Principal{
    TenantID: tenantID,
    ActorID:  reviewerID,
})

result, err := compiler.CompileMultiple(
    ctx,
    "invoice-processing",
    "Invoice processing",
    demonstrations,
    learning.MultiDemoOptions{
        MinimumDemonstrations: 2,
    },
)
```

`result.Candidate` remains in `candidate` status. Publication is a separate
review and release-gate action. `evaluation.Publisher` requires an immutable
`workflow.Review` approving the exact candidate digest as well as a fresh
passing evaluation report. Configure `PublisherOptions.ToolCatalog` for learned
candidates so publication can reject tool contract drift.

The fake HTTP/SSE integration suite runs this extractor through the production
OpenAI Responses, OpenAI Chat Completions, and Anthropic Messages adapters. It
does not require provider credentials.

## Operational limits

Defaults are intentionally bounded:

- 1 MiB redacted input projection, hard maximum 8 MiB;
- 256 KiB streamed model output, hard maximum 2 MiB;
- 4,096 output tokens;
- two provider calls, hard maximum five;
- two-minute timeout per provider call;
- 128 workflow steps;
- three attempts per deterministic tool step;
- ten-minute step timeout.

Only typed retryable provider failures are retried. Invalid model output is not
retried automatically. Extraction is read-only, but duplicate model calls
still increase cost and latency.

Literals are disabled by default because a redacted demonstration cannot
substantiate a model-generated constant. If a tightly controlled workflow
needs literals, set `AllowLiterals` and require human review and evaluation
coverage for every accepted constant.

## Prompt-injection handling

Trust labels are preserved in the projection. Events marked
`untrusted_content` or `model_interpretation` cannot be the sole evidence for
an executable tool or validation step unless
`AllowUntrustedEvidence` is explicitly enabled.

This is one defense layer, not a claim that model isolation is perfect.
Applications must still:

- authenticate observation producers;
- classify source trust conservatively;
- expose only the minimum tool catalog needed for the workflow family;
- validate and review candidates;
- run published workflows through deterministic policy and approval checks;
- evaluate extraction changes before publication.
