# Telemetry

The `telemetry` package converts `core.Observation` callbacks into
vendor-neutral metric and completed-span records. The core SDK does not depend
on OpenTelemetry or a specific backend.

```go
sink, err := telemetry.NewMemorySink(1_000)
if err != nil {
    return err
}

agent, err := skawld.NewAgent(skawld.AgentOptions{
    Provider: provider,
    Model:    model,
    Observer: telemetry.Observer{Sink: sink},
})
```

An observer emits:

- `skawld.operation.count` counters;
- `skawld.operation.duration` histograms;
- `skawld.operation.errors` counters;
- completed spans named `skawld.<observation-type>.<operation>`.

Records carry bounded string attributes for session, run, tenant, actor,
provider, tool, attempt, retryability, and error kind. Raw errors, prompts,
messages, tool input/output, and secrets are never copied into telemetry
records.

`MemorySink` is a bounded, concurrency-safe sink for tests and local
deployments. It drops the oldest record at capacity and reports the cumulative
drop count from `Snapshot`.

Production applications should implement `telemetry.Sink` as a small adapter
to their existing metrics/tracing system. An OpenTelemetry adapter is a useful
optional integration when an application already operates an OTel collector;
it is not a required SDK dependency.

`telemetry.MultiObserver` fans observations out to several `core.Observer`
implementations when an application needs both local diagnostics and external
telemetry.
