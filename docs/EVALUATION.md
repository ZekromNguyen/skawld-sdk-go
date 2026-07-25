# Evaluation

The `evaluation` package provides three separate harnesses:

- deterministic workflow regression;
- end-to-end agent runtime evaluation;
- workflow-extractor evaluation.

They share report conventions and release gates, but they deliberately use
different executors so a deterministic workflow run is never confused with an
agentic model run.

## Workflow evaluation

The workflow runner executes candidate or published workflow versions against
deterministic fixtures. It is intended for local tests and release gates; it
never invokes a real tool or model provider. Candidate status is changed only
inside the isolated executor copy so the normal runtime remains
published-only.

Each scenario supplies:

- task input and workflow context;
- fixture tool descriptors and ordered responses;
- explicit approval decisions;
- expected status, error kind, tool sequence, parameters, and step statuses;
- an optional application assertion over the final execution.

Approvals fail closed. If a workflow requests approval and the scenario does
not explicitly grant it, the runner rejects the action and resumes the
workflow through its normal failure path.

## Metrics

Reports include:

- task success rate;
- step accuracy;
- tool-selection accuracy;
- parameter accuracy;
- unsafe-action rate;
- human-intervention rate;
- retry rate;
- average tool calls;
- average LLM calls;
- average and p95 latency.

Average LLM calls is zero for this harness because it exercises the
deterministic workflow runtime.

Reports contain scenario identifiers and aggregate measurements. Scenario
inputs, tool arguments, fixture outputs, and application state are not copied
into persisted reports.

## Agent runtime evaluation

`AgentRunner` evaluates the real SDK event loop through an `AgentExecutor`.
`SDKAgentExecutor` is the standard adapter: its factory creates an isolated
`skawld.Agent` for each scenario, so the harness remains independent of OpenAI,
Anthropic, Gemini, local-model, or recorded-response providers.

An `AgentScenario` can assert:

- terminal result subtype and stop reason;
- final text when an exact fixture response is appropriate;
- ordered tool names and arguments;
- maximum model calls and input/output token budgets;
- an application-specific assertion over the execution events.

Reports measure task success, tool selection, arguments, unsafe calls, tool
errors, approval intervention, model calls, tokens, and latency. A
consequential call is unsafe when the runtime starts it without a corresponding
permission request. Consequential means high/critical risk, network access, or
an unknown/non-idempotent side effect.

`EventUsage` records completed provider turns. Provider retry attempts that do
not produce a usage event are not model calls in this report; provider-specific
attempt/cost instrumentation belongs in a provider adapter or telemetry sink.

The persisted report contains no prompts, final responses, tool arguments, or
tool results. This is intentional: detailed evidence should remain in an
access-controlled execution trace, not in broadly retained release metrics.

Run the credential-free example:

```sh
go run ./examples/agent_evaluation
```

## Extractor evaluation

`ExtractorRunner` evaluates `Trace -> candidate workflow` behavior through the
vendor-neutral `ExtractorExecutor` contract. It measures:

- extraction and workflow-validation success;
- semantic step accuracy;
- tool-selection accuracy;
- parameter-reference accuracy;
- demonstration/event evidence accuracy;
- unsafe-candidate rate for explicitly marked adversarial cases;
- optional model calls and input/output token usage;
- latency.

`LearningExtractorExecutor` adapts `learning.Extractor`. Rules-based extractors
that implement only that minimal contract have unmeasured usage, so a usage
gate fails closed instead of treating missing instrumentation as zero.
Model-backed extractors can additionally implement `learning.DetailedExtractor`
to report model identity, calls, and tokens without replacing the executor.
`learning/structured.Extractor` implements this detailed contract.

The runner normalizes workflow identity, version, status, and creation time in
the same way as candidate compilation before validation. Reports retain only
case identifiers, measurements, and generic failure codes; demonstrations and
candidate contents are not persisted.

Mark adversarial scenarios with `SecurityCritical: true`, `ExpectError: true`,
and an exact `ExpectedErrorKind`. This prevents a provider outage from being
mistaken for successful security enforcement. If the extractor accepts one,
the case is recorded as an
unsafe candidate and contributes to `MetricUnsafeCandidateRate`. A production
extractor release suite should gate that metric at zero:

```go
evaluation.Gate{
    Metric: evaluation.MetricUnsafeCandidateRate,
    Operator: evaluation.GateAtMost,
    Value: 0,
}
```

Useful adversarial cases include an unknown privileged tool, an
unsubstantiated literal, fabricated evidence, a broadened input contract, and
executable steps supported only by untrusted content.

Run the fixture extractor example:

```sh
go run ./examples/extractor_evaluation
```

## Bounded concurrency

Agent and extractor runners default to one scenario at a time because provider,
tool, and extractor implementations are not automatically concurrency-safe.
Set `MaxConcurrency` only when the supplied executor factory provides isolated
state. Values are bounded to 64, cancellation propagates through the scenario
context, and report order remains the same as suite order.

## Release gates

`evaluation.Gate` applies `at_least` or `at_most` thresholds to named metrics.
A gate on an unmeasured accuracy metric fails rather than silently treating the
metric as perfect.

Typical initial gates are:

```go
[]evaluation.Gate{
    {Metric: evaluation.MetricTaskSuccessRate, Operator: evaluation.GateAtLeast, Value: 0.95},
    {Metric: evaluation.MetricToolSelectionAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
    {Metric: evaluation.MetricParameterAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
    {Metric: evaluation.MetricUnsafeActionRate, Operator: evaluation.GateAtMost, Value: 0},
    {Metric: evaluation.MetricAverageLLMCalls, Operator: evaluation.GateAtMost, Value: 0},
}
```

Gate failure is returned in the report, not as an infrastructure error. This
allows CI or a workflow publication service to decide whether to block a
release while preserving the complete report.

`evaluation.Publisher` is an opt-in guarded publication boundary for workflow
reports. It selects
the latest report for the candidate version and required suite, requires at
least one evaluated gate, rejects failing or stale reports, and also requires
the latest immutable human review for the exact candidate digest to be
approved. Reports and reviews contain the canonical workflow digest, so
neither can be reused for a different document with the same workflow ID and
version. Learned candidates also require the live tool catalog to match their
compiled contract digest. Applications should expose the guarded publisher
rather than the underlying store to release automation.

## Persistence

Pass the corresponding store to each runner to save reports. In-memory stores
are useful for tests. `storage/sqlite.Store` exposes `Evaluations()`,
`AgentEvaluations()`, `ExtractorEvaluations()`, and `Reviews()` for durable,
tenant-isolated report and human-review history.

Run all fixture-only examples:

```sh
go run ./examples/workflow_evaluation
go run ./examples/agent_evaluation
go run ./examples/extractor_evaluation
go run ./examples/learned_invoice
```
