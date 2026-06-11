# Production Readiness

This document tracks the Go SDK's readiness for production use after the
hardening phases (Sprints 9–14). It is the release gate checklist.

## CI Commands

```bash
# Format check
gofmt -l .

# Static analysis
go vet ./...

# Full test suite
go test ./... -count=1 -timeout 300s

# Race detector (requires CGO / gcc)
go test -race ./... -count=1 -timeout 300s

# Targeted benchmarks
go test ./sessions/sqlite -bench=BenchmarkStoreTask -benchmem -benchtime=10s
go test ./providers -bench=BenchmarkTranslate -benchmem -benchtime=10s
go test ./tools -bench=BenchmarkGrep -benchmem -benchtime=10s

# Examples build check
go build ./examples/...
```

## Release Gate Checklist

### Core Runtime

- [x] Abandoned event consumers release active-run state (`RunHandle.Close`)
- [x] Every event emission path can exit through `ctx.Done()`
- [x] Provider streams have one clear terminal error path
- [x] Provider adapters do not block forever on stopped consumers
- [x] Compaction changes provider history only, not complete history
- [x] Context-length failures trigger one forced compaction retry

### Transport & Process Safety

- [x] Shared bounded SSE parser used by providers and MCP
- [x] Maximum SSE event size enforced
- [x] Injectable HTTP clients for providers and MCP
- [x] MCP request IDs are concurrency-safe
- [x] MCP HTTP session IDs are synchronization-safe
- [x] MCP stdio cancelation does not block in json.Decode
- [x] Bash timeout/cancelation waits for process cleanup

### Persistence

- [x] Store operations accept context and respect cancelation
- [x] In-memory store returns deep-copied values
- [x] SQLite task updates are targeted (not full graph replacement)
- [x] Session persistence is backward compatible

### Runtime Ownership

- [x] Agent clones caller-provided tool registries
- [x] Runtime loading (MCP, Skills, Subagents) does not mutate caller registries
- [x] Slow MCP connections do not block unrelated session creation
- [x] Provider concurrency contract is documented
- [x] Filesystem root policy restricts read/write/edit/glob/grep

### Performance

- [x] Token estimation incremental (not full history every turn)
- [x] Grep fallback memory bounded for non-multiline searches
- [x] Benchmark baselines recorded for hot paths

### Observability

- [x] Typed errors preserved through retry and stream boundaries
- [x] Optional structured logging via `*slog.Logger`
- [x] Observer hooks for provider attempts, tool execution, permissions, compaction, MCP, store operations
- [x] Secret redaction rules documented

### Package Boundaries (Phase 16)

- [x] Shared SSE parser in `internal/sse`
- [x] Shared frontmatter parser in `internal/frontmatter`
- [x] Shared deep-copy in `internal/jsoncopy`
- [x] Config decoupled from provider construction via `ProviderFactory`
- [x] Typed input parsers wired into built-in tools
- [x] Internal names normalized (`providerHistory`, `completeHistory`, `SubagentsDir`)
- [x] Package layout documented in `PACKAGE_LAYOUT.md`

## Known Gaps

1. **Race detector tests**: require CGO and `gcc`; blocked in Windows CI environments without a C compiler.
2. **Large-scale stress tests**: run sessions with 10,000+ messages, 1,000+ concurrent sessions, and 100+ subagents.
3. **Provider integration tests**: currently use fake HTTP servers; real-provider integration requires API keys.
4. **Windows long-path support**: filesystem tools do not yet handle Windows paths longer than `MAX_PATH`.

## Stress Test Scenarios

The stress test fixtures in `stress_test.go` cover:

- Concurrent session creation with slow MCP/skill loaders
- Parent and subagent provider concurrent use
- Rapid session create/delete cycles
- Task dependency cycle detection under concurrent updates
- Bash cancelation and cleanup timing

## Re-Score

After Sprint 14 (2026-06-11), the SDK is rated **8/10** for production readiness.

- -1: Race detector not runnable in all environments (CGO requirement).
- -1: Large-scale concurrency tests not yet automated.