# Migration TODO

This list tracks the remaining work needed to reach feature parity with the
original TypeScript `@skawld/agent-sdk`.

Status reviewed against the current Go codebase and tests on 2026-07-26.

For phased Scrum-style delivery planning, see [`SCRUM_PLAN.md`](./SCRUM_PLAN.md).

## Production Audit Backlog

This backlog is based on a production-readiness audit of the current Go
codebase. Items are prioritized by risk to correctness, scalability,
maintainability, and operational safety.

### Critical

- [x] Redesign run event delivery so abandoned `Session.Run` consumers cannot leak goroutines.
  - Affected code: `session.go:Run`, `loop.go:runLoop`, `loop.go:streamTurnAttempt`, `loop.go:executeParallelBatch`, `session.go:compactProviderView`, `skills_runtime.go:invokeSkill`, and `subagents_runtime.go:runSubagent`.
  - Problem: `Session.Run` returns a receive-only event channel, but the SDK has no explicit close/abort handle for callers that stop reading. Many code paths write directly to `out <- ev`. If the caller abandons the channel after the 64-slot buffer fills, the run goroutine, tool goroutines, provider stream readers, and active-run cleanup can block indefinitely.
  - Impact: leaked goroutines, stuck `active` state, retained provider/tool resources, and backpressure from one abandoned run affecting later work in the same session.
  - Improve: introduce a `RunHandle` or iterator with `Events()`, `Abort()`, and `Close()`; route all event writes through a context-aware emitter; add tests that intentionally stop reading after the first event.
  - Rewrite sketch:
    ```go
    type RunHandle struct {
        events <-chan core.Event
        cancel context.CancelFunc
    }

    func (h *RunHandle) Events() <-chan core.Event { return h.events }
    func (h *RunHandle) Abort()                    { h.cancel() }
    func (h *RunHandle) Close()                    { h.cancel() }

    func emit(ctx context.Context, out chan<- core.Event, ev core.Event) error {
        select {
        case out <- ev:
            return nil
        case <-ctx.Done():
            return ctx.Err()
        }
    }
    ```
  - Why better: cancellation becomes part of the public lifecycle, and every event send has a bounded exit path.
  - Tradeoff: API surface grows; keep `Run` as a compatibility wrapper that returns `handle.Events()`.

- [x] Replace the provider dual-channel stream contract with a single pull-based stream.
  - Affected code: `core/provider.go`, `loop.go:streamTurnAttempt`, `providers/anthropic.go:Stream`, `providers/openai_chat.go:Stream`, `providers/openai_responses.go:Stream`, `providers/sse.go:postSSE`.
  - Problem: providers return `(<-chan ProviderStreamEvent, <-chan error)`, and `streamTurnAttempt` waits for `events` to close before reading one value from `errs`. This contract is fragile: a provider can block sending an error while the loop is waiting on events, or lose cancellation when direct event sends block.
  - Impact: stream deadlocks are hard to diagnose, provider implementations are easy to get wrong, and retry behavior depends on channel-close ordering rather than a clear protocol.
  - Improve: expose `Recv() (ProviderStreamEvent, error)` and `Close() error`, or use one channel carrying `{Event, Err}`. The loop should select on context and stop immediately on first terminal error.
  - Rewrite sketch:
    ```go
    type ProviderStream interface {
        Recv() (core.ProviderStreamEvent, error) // returns io.EOF when done
        Close() error
    }

    type Provider interface {
        ID() string
        ContextWindow(model ModelID) int
        Stream(ctx context.Context, req ProviderRequest) (ProviderStream, error)
    }
    ```
  - Why better: one terminal path removes channel ordering bugs and makes provider adapters easier to test.
  - Tradeoff: this is a breaking internal/provider API change, so ship it behind an adapter during migration.

- [x] Make MCP client transports concurrency-safe and cancellation-aware.
  - Affected code: `tools/mcp/client.go:Client.request`, `tools/mcp/client.go:httpTransport.Request`, `tools/mcp/client.go:httpTransport.Notify`, `tools/mcp/client.go:stdioTransport.Request`, `tools/mcp/client.go:decodeSSEResponse`.
  - Problem: `Client.nextID` is incremented without synchronization; `httpTransport.sessionID` is read and written without a mutex; `stdioTransport.Request` checks context before `json.Decoder.Decode` but cannot interrupt a blocked decode; MCP SSE uses `bufio.Scanner`.
  - Impact: concurrent MCP tool calls can race request IDs and session headers, stdio servers can leave callers blocked after cancellation, and large SSE messages can fail at the scanner token limit.
  - Improve: use `atomic.Int64` or a mutex for IDs, protect `sessionID`, move stdio reads to a single demultiplexing goroutine keyed by request ID, close the process/pipe on context cancellation, and parse SSE with `bufio.Reader`.
  - Rewrite sketch:
    ```go
    type Client struct {
        name      string
        transport transport
        nextID    atomic.Int64
    }

    func (c *Client) request(ctx context.Context, method string, params any) (map[string]any, error) {
        id := c.nextID.Add(1)
        return c.transport.Request(ctx, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
    }

    type httpTransport struct {
        client   *http.Client
        endpoint string
        headers  map[string]string
        mu       sync.RWMutex
        sessionID string
    }
    ```
  - Why better: request identity and session state become race-free under `ToolConcurrency > 1`.
  - Tradeoff: a demultiplexed stdio transport is more code, but it matches JSON-RPC concurrency semantics.

- [x] Add `context.Context` to the session store interface and remove internal `context.Background()` database calls.
  - Affected code: `core/store.go`, `sessions/sqlite/store.go:Create`, `AppendMessages`, `UpdateMeta`, `SetInvokedSkills`, `CreateTask`, `UpdateTask`, `DeleteTask`, `loadTasks`, plus call sites in `session.go`, `agent.go`, and task tools.
  - Problem: SQLite operations start transactions with `context.Background()` and query without caller deadlines. The public store interface also has no way to propagate cancellation.
  - Impact: a canceled run can continue waiting on database locks or slow I/O, tying up goroutines and making shutdown unreliable.
  - Improve: make all store methods context-aware, use `BeginTx(ctx, nil)`, `QueryContext`, `ExecContext`, and `QueryRowContext`, and thread the run/session context through all call sites.
  - Rewrite sketch:
    ```go
    type SessionStore interface {
        Create(ctx context.Context, id string, meta map[string]any) (SessionRecord, error)
        AppendMessages(ctx context.Context, id string, messages []Message) ([]StoredMessage, error)
        UpdateTask(ctx context.Context, sessionID, taskID string, patch TaskPatch) (Task, bool, error)
        Close() error
    }

    func (s *Store) AppendMessages(ctx context.Context, id string, messages []core.Message) ([]core.StoredMessage, error) {
        tx, err := s.db.BeginTx(ctx, nil)
        if err != nil {
            return nil, fmt.Errorf("begin append messages: %w", err)
        }
        defer rollback(tx)
        // use tx.QueryRowContext and tx.ExecContext below
        return appended, tx.Commit()
    }
    ```
  - Why better: cancellation, timeouts, and shutdown now reach persistence work.
  - Tradeoff: broad signature change; use a compatibility adapter for existing custom stores.

- [x] Ensure `BashTool.Execute` waits for process cleanup after timeout or cancellation.
  - Affected code: `tools/bash.go:Execute`, `tools/helpers.go:taskKillTree`.
  - Problem: on timeout/cancel the tool kills the process tree and returns immediately while the `cmd.Wait` goroutine can still be running. On Unix, `taskKillTree` sleeps for two seconds unconditionally before `SIGKILL`.
  - Impact: command output writers can still be active after the tool result is emitted, process resources can linger, and cancellation consumes worker time unnecessarily.
  - Improve: use `exec.CommandContext` with a context deadline, set `cmd.Cancel` or kill the process group, wait for `cmd.Wait` with a short grace timer, and avoid a fixed sleep when the process has already exited.
  - Rewrite sketch:
    ```go
    runCtx, cancel := context.WithTimeout(ctx.Context, time.Duration(timeoutMs)*time.Millisecond)
    defer cancel()

    cmd := exec.CommandContext(runCtx, shell, shellFlag, command)
    setupProcessOptions(cmd)

    if err := cmd.Start(); err != nil {
        return spawnError(err), nil
    }
    err := waitWithTreeKill(runCtx, cmd, 500*time.Millisecond)
    ```
  - Why better: process lifetime is joined to tool lifetime and goroutine cleanup is deterministic.
  - Tradeoff: platform-specific process-group logic needs stronger tests on Windows and Unix.

### High

- [x] Replace `bufio.Scanner` SSE parsing with a streaming reader that supports large events.
  - Affected code: `providers/sse.go:postSSE`, `tools/mcp/client.go:decodeSSEResponse`.
  - Problem: `bufio.Scanner` has a default 64 KiB token limit. Large model deltas, MCP resource payloads, or metadata events can fail with `token too long`.
  - Impact: valid provider or MCP responses can terminate streams in production, especially with large tool arguments or reasoning metadata.
  - Improve: use `bufio.Reader.ReadString('\n')` or `ReadBytes('\n')`, cap accumulated event size explicitly, preserve multi-line SSE data semantics, and return parse errors with context.
  - Rewrite sketch:
    ```go
    func readSSE(ctx context.Context, r io.Reader, maxEvent int, handle func(string) error) error {
        br := bufio.NewReader(r)
        var data strings.Builder
        for {
            line, err := br.ReadString('\n')
            if len(line) > 0 {
                line = strings.TrimRight(line, "\r\n")
                // collect data: lines and dispatch on blank line
            }
            if err != nil {
                if errors.Is(err, io.EOF) {
                    return nil
                }
                return err
            }
            if data.Len() > maxEvent {
                return fmt.Errorf("sse event exceeds %d bytes", maxEvent)
            }
        }
    }
    ```
  - Why better: large events fail predictably at an SDK-defined limit instead of an implicit scanner limit.
  - Tradeoff: more parser code; add fixtures for multi-line, CRLF, and oversized events.

- [x] Inject and reuse HTTP clients with timeouts and shared transports.
  - Affected code: `providers/sse.go:httpClient`, `providers/anthropic.go:Stream`, `providers/openai_chat.go:Stream`, `providers/openai_responses.go:Stream`, `tools/mcp/client.go:newHTTPTransport`.
  - Problem: providers create a new `http.Client{Timeout: 0}` per stream; MCP creates a default client with no timeout. There is no `HTTPDoer` injection point for tests, proxies, tracing, or production tuning.
  - Impact: no request deadline unless the context is honored, weaker connection reuse, harder observability, and less control over TLS/proxy/retry behavior.
  - Improve: add `HTTPClient HTTPDoer` and optional timeout fields to provider/MCP options; default to a package-level client with a tuned `http.Transport`.
  - Rewrite sketch:
    ```go
    type HTTPDoer interface {
        Do(*http.Request) (*http.Response, error)
    }

    var defaultHTTPClient = &http.Client{
        Timeout: 5 * time.Minute,
        Transport: &http.Transport{
            MaxIdleConns:        100,
            MaxIdleConnsPerHost: 20,
            IdleConnTimeout:     90 * time.Second,
        },
    }
    ```
  - Why better: fewer allocations, predictable deadlines, and easier integration with tracing or custom transports.
  - Tradeoff: stream timeouts need to be configurable because long model streams can exceed a fixed timeout.

- [x] Make all provider adapter sends context-aware.
  - Affected code: direct `out <-` in `providers/anthropic.go:Stream`, `providers/openai_chat.go:Stream`, and `providers/openai_responses.go:Stream`.
  - Problem: provider goroutines send `message_start`, deltas, tool events, and `message_end` without selecting on `ctx.Done()`.
  - Impact: if the loop stops reading, provider goroutines can block even after the HTTP request has been canceled.
  - Improve: add a provider-local send helper and return immediately on cancellation.
  - Rewrite sketch:
    ```go
    func sendProvider(ctx context.Context, out chan<- core.ProviderStreamEvent, ev core.ProviderStreamEvent) bool {
        select {
        case out <- ev:
            return true
        case <-ctx.Done():
            return false
        }
    }
    ```
  - Why better: providers respect the same lifecycle as the run loop.
  - Tradeoff: adapter code becomes slightly more verbose until the provider stream API is redesigned.

- [x] Reduce lock scope in `Agent.Session` and stop mutating caller-owned registries during lazy loading.
  - Affected code: `agent.go:Session`, `agent.go:connectMCP`, `agent.go:loadSkills`, `agent.go:loadSubagents`, `AgentOptions.Tools`.
  - Problem: `Agent.Session` holds `sessionsMu` while connecting MCP servers, loading skill/subagent files, registering tools, creating/loading sessions, and reading messages. It also registers MCP, Skill, and Subagent tools into `opts.Tools`, which may be a registry owned by the caller or shared across agents.
  - Impact: slow MCP or filesystem work serializes all session creation; shared registries can be mutated unexpectedly and may race if used by multiple agents.
  - Improve: clone the registry in `NewAgent`, use `sync.OnceValue` or separate once guards for lazy resources, load outside the session creation lock, and protect system prompt rebuilds with their own mutex.
  - Rewrite sketch:
    ```go
    type Agent struct {
        tools *tools.Registry
        loadRuntimeOnce func() error
        systemMu sync.RWMutex
    }

    func NewAgent(opts AgentOptions) (*Agent, error) {
        reg := opts.Tools.Clone()
        a := &Agent{tools: reg}
        a.loadRuntimeOnce = sync.OnceValue(func() error {
            return a.loadRuntimeResources(context.Background())
        })
        return a, nil
    }
    ```
  - Why better: session creation no longer blocks on unrelated setup, and tool ownership is explicit.
  - Tradeoff: `sync.OnceValue` cannot accept a per-call context; for context-sensitive retries, use `sync.Once` plus a guarded state machine instead.

- [x] Define and enforce a provider concurrency contract for subagents.
  - Affected code: `subagents_runtime.go:runSubagent`, provider implementations.
  - Problem: a child `Agent` reuses `s.agent.opts.Provider` concurrently with the parent. The `core.Provider` interface does not say whether implementations must be safe for concurrent `Stream` calls.
  - Impact: custom providers with mutable state can race or corrupt streams when subagents run alongside parent/tool work.
  - Improve: document provider concurrency requirements, add race tests with concurrent subagent streams, or provide a `ProviderFactory`/`Clone` hook for per-agent providers.
  - Rewrite sketch:
    ```go
    type ProviderFactory interface {
        NewProvider() core.Provider
    }

    provider := s.agent.opts.Provider
    if f := s.agent.opts.ProviderFactory; f != nil {
        provider = f.NewProvider()
    }
    ```
  - Why better: SDK behavior is explicit for high-concurrency subagent workflows.
  - Tradeoff: cloning providers may duplicate connection pools unless clients/transports are shared intentionally.

- [x] Prevent permission callback goroutine leaks and improve callback observability.
  - Affected code: `permissions/engine.go:callPermissionCallback`.
  - Problem: `callPermissionCallback` starts a goroutine per callback. If the callback ignores context, `Resolve` returns on cancellation while the callback goroutine continues until it finishes.
  - Impact: abandoned UI/API permission prompts can accumulate goroutines and hide slow callback behavior.
  - Improve: prefer synchronous callback execution when the caller accepts blocking, or require callbacks to honor context and record callback duration. If asynchronous behavior is needed, use a bounded callback worker pool.
  - Rewrite sketch:
    ```go
    start := time.Now()
    resp, err := e.opts.CanUseTool(ctx, req)
    if err != nil {
        return CanUseToolResponse{}, fmt.Errorf("permission callback after %s: %w", time.Since(start), err)
    }
    ```
  - Why better: no unbounded goroutine creation inside permission resolution.
  - Tradeoff: a bad callback can block `Resolve`; document that callbacks must honor context or configure a callback timeout wrapper.

- [x] Deep-copy mutable session/task/message data in the in-memory store.
  - Affected code: `sessions/memory.go:Create`, `Load`, `AppendMessages`, `LoadMessages`, `UpdateMeta`, `CreateTask`, `GetTask`, `ListTasks`.
  - Problem: records, messages, tasks, metadata maps, provider metadata maps, and content input maps are returned shallowly in several paths.
  - Impact: callers can mutate stored state without going through store methods, creating data races and test flakiness.
  - Improve: centralize clone helpers for `SessionRecord`, `Message`, `ContentBlock`, `Task`, and JSON-like maps/slices; use them on both input and output boundaries.
  - Rewrite sketch:
    ```go
    func cloneMap(in map[string]any) map[string]any {
        if in == nil {
            return nil
        }
        out := make(map[string]any, len(in))
        for k, v := range in {
            out[k] = cloneJSONValue(v)
        }
        return out
    }
    ```
  - Why better: store state cannot be changed by external references.
  - Tradeoff: extra allocations; acceptable for test/in-memory correctness and can be optimized with targeted clones.

- [x] Replace full task-graph replacement in SQLite updates with targeted SQL mutations.
  - Affected code: `sessions/sqlite/store.go:UpdateTask`, `DeleteTask`, `loadTasksTx`, `replaceTasksTx`.
  - Problem: updating one task loads every task and edge for the session, mutates in memory, deletes all task edges, and upserts every task.
  - Impact: O(n) writes per task update, lock amplification, poor behavior for sessions with many tasks, and higher conflict risk with concurrent users.
  - Improve: load only affected tasks and edges, validate cycles with a recursive CTE or bounded graph query, update one task row, and insert/delete only requested edges.
  - Rewrite sketch:
    ```sql
    UPDATE tasks
    SET subject = COALESCE(?, subject),
        description = COALESCE(?, description),
        updated_at = ?
    WHERE session_id = ? AND id = ?;

    DELETE FROM task_edges
    WHERE session_id = ? AND blocker_id = ? AND blocked_id IN (...);
    ```
  - Why better: task updates scale with the patch size instead of total task count.
  - Tradeoff: cycle detection becomes more SQL-heavy; keep the current in-memory algorithm as a fallback for small stores if needed.

- [x] Stream and bound grep fallback memory use.
  - Affected code: `tools/grep.go:runGrepFallback`.
  - Problem: the fallback reads each file fully with `os.ReadFile`, stores all matching file results, and stores full `fileLines` for content mode.
  - Impact: scanning large repos can allocate heavily or exhaust memory when ripgrep is unavailable.
  - Improve: process files with `bufio.Scanner`/`Reader` using a larger buffer, stream matches into a bounded renderer, stop after `head_limit`, and use `io.LimitReader` for binary detection.
  - Rewrite sketch:
    ```go
    func scanFile(ctx context.Context, path string, re *regexp.Regexp, emit func(matchLine) bool) error {
        f, err := os.Open(path)
        if err != nil {
            return err
        }
        defer f.Close()
        br := bufio.NewReader(f)
        for lineNo := 1; ; lineNo++ {
            line, err := br.ReadString('\n')
            if re.MatchString(line) && !emit(matchLine{lineNo: lineNo, text: strings.TrimRight(line, "\r\n")}) {
                return nil
            }
            if errors.Is(err, io.EOF) {
                return nil
            }
            if err != nil {
                return err
            }
        }
    }
    ```
  - Why better: memory is bounded by the output limit and current file buffer.
  - Tradeoff: multiline regex support still needs full-file or chunked-window logic; keep full-file mode only when `multiline` is true.

### Medium

- [x] Avoid repeated JSON marshaling for token estimates and provider request construction.
  - Affected code: `session.go:estimateProviderTokens`, `session.go:compactProviderView`, `loop.go:buildProviderRequest`.
  - Problem: every turn can marshal all tools and all messages just to estimate tokens before proactive compaction.
  - Impact: long sessions pay O(history size) CPU and allocation cost on every turn even when compaction is not close.
  - Improve: cache stable tool/system estimates, track approximate message token counts when appending messages, and only recompute exactly near the threshold.
  - Rewrite sketch:
    ```go
    type Session struct {
        tokenMu sync.Mutex
        estimatedProviderTokens int
    }

    func (s *Session) append(ctx context.Context, messages []core.Message) error {
        delta := estimateMessages(messages)
        s.tokenMu.Lock()
        s.estimatedProviderTokens += delta
        s.tokenMu.Unlock()
        return s.store.AppendMessages(ctx, s.ID, messages)
    }
    ```
  - Why better: steady-state turn cost is proportional to new messages.
  - Tradeoff: estimates can drift; periodically recompute or reset after compaction.

- [x] Reduce weak `map[string]interface{}` usage at tool and provider boundaries.
  - Affected code: `core/tool.go`, tool `Validate` methods, provider translation code, MCP conversion.
  - Problem: internal code repeatedly asserts values like `input["command"].(string)` after validation. Public schemas and dynamic provider payloads still need JSON-like maps, but validated runtime inputs do not.
  - Impact: readability suffers, invalid inputs can panic if a caller bypasses validation, and refactors are hard because field names are stringly typed.
  - Improve: add typed input structs per built-in tool and provider wire structs where stable. Keep `map[string]any` only at external JSON/schema boundaries.
  - Rewrite sketch:
    ```go
    type BashInput struct {
        Command     string
        TimeoutMS   int
        Description string
    }

    func parseBashInput(raw map[string]any) (BashInput, error) {
        command, ok := raw["command"].(string)
        if !ok || strings.TrimSpace(command) == "" {
            return BashInput{}, core.NewToolExecutionError("Bash", "command must be a non-empty string")
        }
        return BashInput{Command: command, TimeoutMS: timeout}, nil
    }
    ```
  - Why better: implementation code becomes type-safe after one parsing step.
  - Tradeoff: the generic `core.Tool` interface still needs dynamic maps for third-party tools; typed helpers should be optional.

- [x] Improve error wrapping and typed error preservation across tools and stores.
  - Affected code: `tools/*.go`, `sessions/sqlite/store.go`, `tools/mcp/client.go`, `permissions/engine.go`, provider translators.
  - Problem: many errors are returned as plain strings in `ToolResult`, several store/provider errors are returned without `%w`, and MCP close joins strings instead of preserving causes.
  - Impact: callers cannot reliably use `errors.Is`/`errors.As`, logs lose cause chains, and retry/observability logic is weaker.
  - Improve: wrap internal errors with operation context, preserve `*core.SkawldError`, and use `errors.Join` for multi-close failures.
  - Rewrite sketch:
    ```go
    if err := tx.Commit(); err != nil {
        return core.SessionRecord{}, fmt.Errorf("commit create session %q: %w", id, err)
    }

    return errors.Join(closeErrs...)
    ```
  - Why better: higher layers can classify errors without parsing strings.
  - Tradeoff: tool results still need user-facing strings; keep typed errors for SDK callers and render separately for model-facing content.

- [x] Add structured logging and observability hooks.
  - Affected code: `AgentOptions`, `loop.go`, provider adapters, MCP manager, stores, tools.
  - Problem: operational visibility is available only through the event stream. There is no `slog.Logger`, trace hook, request ID propagation, or metrics surface for provider latency, retries, tool duration, permission latency, compaction, and store timings.
  - Impact: production incidents require reproducing event streams instead of using normal logs/metrics/traces.
  - Improve: add optional `*slog.Logger` and small observer interface; log with stable keys such as `session_id`, `run_id`, `tool`, `provider`, `attempt`, and `duration_ms`.
  - Rewrite sketch:
    ```go
    type Observer interface {
        ProviderAttempt(ctx context.Context, info ProviderAttemptInfo)
        ToolFinished(ctx context.Context, info ToolFinishedInfo)
    }

    logger.InfoContext(ctx, "tool finished",
        slog.String("session_id", s.ID),
        slog.String("tool", call.tool.Name()),
        slog.Int64("duration_ms", time.Since(start).Milliseconds()),
        slog.Bool("error", isErr),
    )
    ```
  - Why better: runtime behavior becomes visible without changing the event API.
  - Tradeoff: logging must avoid secrets and large tool/provider payloads by default.

- [x] Unify skill and subagent frontmatter parsing.
  - Affected code: `skills/loader.go:parseFrontmatter`, `subagents/loader.go:parseFrontmatter`.
  - Problem: the same ad hoc YAML-like parser is duplicated and supports only a small subset of YAML.
  - Impact: divergent behavior is likely, quoted values/lists are fragile, and metadata validation is weak.
  - Improve: move parsing to an internal `frontmatter` package and use `gopkg.in/yaml.v3` or a deliberately documented mini-parser with shared tests.
  - Rewrite sketch:
    ```go
    type Document[T any] struct {
        Meta T
        Body string
    }

    func Parse[T any](raw []byte) (Document[T], error) {
        // split frontmatter, yaml.Unmarshal into T, trim body
    }
    ```
  - Why better: one parser, typed metadata, and consistent validation across runtime resources.
  - Tradeoff: a YAML dependency adds weight; a shared mini-parser avoids dependency cost but remains limited.

- [x] Separate config loading from provider construction.
  - Affected code: `config/config.go:File.AgentOptions`, `config/config.go:provider`.
  - Problem: the config package imports concrete provider implementations and constructs them directly.
  - Impact: config parsing is tightly coupled to provider packages, custom providers cannot participate without bypassing config, and validation of secrets/base URLs is minimal.
  - Improve: split parse/validate from binding. Return a typed config value, then let a provider registry/factory build concrete providers.
  - Rewrite sketch:
    ```go
    type ProviderFactory func(ProviderConfig) (core.Provider, error)

    type Binder struct {
        Providers map[string]ProviderFactory
    }

    func (b Binder) AgentOptions(c File) (AgentOptions, error) {
        factory := b.Providers[c.Provider]
        provider, err := factory(c.ProviderConfig())
        // ...
    }
    ```
  - Why better: package boundaries are cleaner and tests can bind fake providers without config knowing them.
  - Tradeoff: slightly more setup for simple examples.

- [x] Add explicit workspace/root policy for filesystem tools.
  - Affected code: `tools/helpers.go:resolvePath`, `tools/read.go`, `tools/write.go`, `tools/edit.go`, `tools/glob.go`, `tools/grep.go`, `permissions/engine.go:matchPathRule`.
  - Problem: paths are resolved relative to `CWD`, but absolute paths are allowed. Permissions can restrict paths, yet the tool layer itself has no root jail or symlink policy.
  - Impact: in embedded/server use, a misconfigured permission policy can allow reads/writes outside the intended project root.
  - Improve: add a configurable `FilesystemPolicy` with allowed roots, symlink handling, and path normalization based on `filepath.EvalSymlinks` where appropriate.
  - Rewrite sketch:
    ```go
    type FilesystemPolicy struct {
        Roots []string
        FollowSymlinks bool
    }

    func (p FilesystemPolicy) Resolve(cwd, raw string) (string, error) {
        abs := resolvePath(raw, cwd)
        checked := abs
        if p.FollowSymlinks {
            checked, _ = filepath.EvalSymlinks(abs)
        }
        if !withinAnyRoot(checked, p.Roots) {
            return "", core.NewPermissionError("path outside allowed roots")
        }
        return abs, nil
    }
    ```
  - Why better: security does not depend only on model-facing permission rules.
  - Tradeoff: some users intentionally operate outside `CWD`; make the policy configurable and default-compatible.

- [x] Use `slices` and `maps` package helpers for clearer collection code.
  - Affected code: repeated contains/clone/sort helpers in `permissions`, `sessions`, `tools`, `providers`.
  - Problem: hand-written collection helpers are duplicated and increase small bug surface.
  - Impact: maintainability cost and inconsistent clone/contains behavior.
  - Improve: use `slices.Contains`, `slices.SortFunc`, `maps.Clone`, and shared deep-copy helpers where shallow clones are insufficient.
  - Rewrite sketch:
    ```go
    if !slices.Contains(tools, call.Tool.Name()) {
        return false
    }
    headers := maps.Clone(cfg.Headers)
    ```
  - Why better: standard library helpers communicate intent and reduce utility code.
  - Tradeoff: only use `maps.Clone` for shallow copies; JSON-like values still need deep clone.

### Low

- [x] Add benchmarks for known hot paths before major refactors.
  - Targets: `runLoop` scheduling with many tool calls, provider translation functions, `estimateProviderTokens`, `Glob` and `Grep` fallback on large trees, SQLite task updates with 100/1,000/10,000 tasks, MCP SSE parsing, compaction request construction.
  - Problem: performance-sensitive code has tests but no benchmark baselines.
  - Impact: optimization work cannot prove improvements or catch regressions.
  - Improve: add focused `Benchmark*` tests and keep fixtures deterministic.
  - Rewrite sketch:
    ```go
    func BenchmarkEstimateProviderTokens(b *testing.B) {
        messages := makeLargeHistory(1000)
        tools := makeToolSchemas(50)
        b.ReportAllocs()
        for i := 0; i < b.N; i++ {
            _ = estimateProviderTokens(nil, tools, messages)
        }
    }
    ```
  - Why better: refactors are guided by data.
  - Tradeoff: benchmark fixtures need maintenance as core types evolve.

- [x] Add race/leak tests for cancellation and abandoned streams.
  - Targets: abandoned `Session.Run`, provider error during partial output, canceled permission callback, canceled MCP stdio request, canceled `Bash`, parallel MCP tool calls, subagent parent cancellation.
  - Problem: current tests cover behavior but not enough lifecycle failure modes.
  - Impact: goroutine leaks and races can pass normal `go test ./...`.
  - Improve: use `go test -race`, goroutine-count checks around controlled fakes, and fake readers/transports that block until cancellation.
  - Rewrite sketch:
    ```go
    func TestRunAbandonedConsumerDoesNotLeak(t *testing.T) {
        before := runtime.NumGoroutine()
        ch := session.Run(ctx, "prompt", RunOptions{})
        <-ch
        cancel()
        eventually(t, func() bool { return runtime.NumGoroutine() <= before+delta })
    }
    ```
  - Why better: lifecycle fixes stay fixed.
  - Tradeoff: goroutine-count tests can be flaky; prefer explicit done channels where possible.

- [x] Normalize naming around sessions, stores, and runtime resources.
  - Affected code: `AgentOptions`, `Session`, `providerView`, `fullHistory`, `MCPServers`, `AgentsDir`, `Subagent`.
  - Problem: some names mix public config language with implementation details. `providerView` and `fullHistory` are meaningful only after reading compaction code, and `AgentsDir` refers specifically to subagent definitions.
  - Impact: onboarding and maintenance are harder than necessary.
  - Improve: rename internal fields to clarify ownership and lifecycle, such as `providerHistory`, `completeHistory`, `SubagentsDir`, and `runtimeTools`.
  - Rewrite sketch:
    ```go
    type Session struct {
        providerHistory []core.Message
        completeHistory []core.Message
    }
    ```
  - Why better: field names explain their role without requiring comments.
  - Tradeoff: public option renames require deprecation aliases.

- [x] Document the public concurrency contract.
  - Affected code: package docs in `doc.go`, `core/provider.go`, `core/tool.go`, `tools/registry.go`, `sessions`.
  - Problem: it is not clear which types are safe for concurrent use: `Agent`, `Session`, `Provider`, `Tool`, `Registry`, `SessionStore`, MCP clients.
  - Impact: SDK users can accidentally share unsafe implementations across agents or sessions.
  - Improve: add Go doc comments and tests that enforce the intended contract.
  - Rewrite sketch:
    ```go
    // Provider implementations must be safe for concurrent Stream calls.
    // Stream must return promptly when ctx is canceled.
    type Provider interface { ... }
    ```
  - Why better: concurrency expectations become part of the API.
  - Tradeoff: stronger contracts may force changes in custom user implementations.

### Top 10 Improvements

1. Add an explicit run handle and context-aware event emission to prevent abandoned-consumer goroutine leaks.
2. Replace provider dual-channel streaming with a single stream abstraction.
3. Fix MCP request ID/session races and stdio cancellation.
4. Add context-aware store methods and remove `context.Background()` from SQLite operations.
5. Make Bash process cleanup wait for `cmd.Wait` and avoid fixed cancellation sleeps.
6. Replace scanner-based SSE parsing with bounded reader-based parsing.
7. Inject shared HTTP clients with timeouts, tuned transports, and observability hooks.
8. Clone tool registries and reduce `Agent.Session` lock scope during MCP/skill/subagent loading.
9. Replace SQLite full task-graph replacement with targeted updates.
10. Add race/leak tests and benchmarks for run lifecycle, provider translation, grep fallback, and SQLite task updates.

### Overall Architecture Assessment

The SDK has a clear high-level architecture: public `Agent`/`Session` orchestration,
provider adapters, pluggable tools, stores, permissions, MCP, skills, and subagents.
The main architectural risk is that lifecycle management is channel-based but not
uniformly cancellation-aware. The second major risk is that dynamic JSON-like maps
are used deep into implementation code instead of being parsed at package
boundaries. The third risk is lazy runtime loading mutating shared tool registries
under a broad session lock.

### Performance Optimization Roadmap

1. Benchmark current hot paths before refactoring.
2. Fix cancellation and stream lifecycle issues first; leaks dominate all other performance concerns.
3. Reuse HTTP clients and replace scanner SSE parsing.
4. Cache stable token estimate inputs and track message estimate deltas.
5. Stream grep fallback and stop after output limits.
6. Replace SQLite task full replacement with patch-sized mutations.
7. Profile provider translation allocations and replace repeated string concatenation with `strings.Builder` where hot.

### Refactoring Roadmap

1. Add context-aware emit helpers and run lifecycle tests without changing public API.
2. Add compatibility adapters for old/new provider stream interfaces.
3. Introduce context-aware store interfaces with adapters for existing stores.
4. Clone registries in `NewAgent` and isolate runtime tool registration.
5. Move shared frontmatter parsing and deep-copy helpers into internal packages.
6. Add typed built-in tool input parsers while keeping the generic `core.Tool` API.
7. Add observability hooks after lifecycle behavior is stable.

### Suggested Folder And Package Structure

```text
core/                 Stable public contracts and event/message types
agent/ or internal/runtime/
                      Run loop, scheduler, compaction orchestration
providers/            Provider implementations and provider-specific wire structs
internal/sse/          Shared bounded SSE parser
internal/frontmatter/  Shared skill/subagent frontmatter parser
internal/clone/        Deep-copy helpers for JSON-like SDK values
tools/                Built-in local tools
tools/mcp/            MCP manager, transports, and MCP tool adapter
sessions/             Store interfaces/adapters and in-memory store
sessions/sqlite/      SQLite implementation
permissions/          Permission engine and rule matching
config/               Config parsing only
config/bind/          Optional provider/tool binding from parsed config
```

### Production-Readiness Score

Current score: 6/10.

The feature surface is broad and tests cover many parity scenarios, but the SDK is
not yet production-grade for high-scale or long-running embedded use until the
run lifecycle, provider stream contract, MCP races, context propagation, process
cleanup, and observability gaps are addressed.

## Core Engine

- [x] Add true compaction support:
  - [x] `CompactionStrategy` interface
  - [x] default keep-last-10-turns summarizer
  - [x] forced compaction after context-length provider errors
  - [x] `compaction` event emission
- [x] Improve scheduler parity:
  - [x] adjacent-batch partitioning by `parallelSafe`
  - [x] bounded concurrent read-tool execution
  - [x] synthetic `tool_call_end` events on abort
  - [x] event interleaving from tools that emit nested events
- [x] Add run abort API equivalent to `Session.abort()`.
- [x] Add one active run cleanup semantics for abandoned iterators.
- [x] Preserve provider metadata emitted by providers across session
      append/resume.
- [x] Add full partial-assistant coverage for text, thinking, and tool JSON deltas.
- [x] Add `maxRetries` retry loop parity around provider streams.
- [x] Add context window based proactive compaction threshold logic.

## Providers

- [x] Ship baseline streaming adapters for Anthropic Messages, OpenAI Chat
      Completions, and OpenAI Responses.
- [x] Harden Anthropic adapter:
  - [x] exact Messages API wire translation for all content block types
  - [x] prompt caching breakpoints and TTL support
  - [x] thinking and effort support
  - [x] complete usage mapping including cache read/write tokens
  - [x] Anthropic error mapping to typed Skawld errors
- [x] Harden OpenAI Chat Completions adapter:
  - [x] image block translation
  - [x] tool-result image fallback behavior
  - [x] OpenAI-compatible base URL, default header, and context-window options
  - [x] typed error mapping and retry-after parsing
- [x] Harden OpenAI Responses adapter:
  - [x] basic text/function-call SSE streaming
  - [x] response metadata capture for `response_id` and `output_items`
  - [x] previous response id support
  - [x] reasoning summary and encrypted reasoning support
  - [x] output item metadata replay
  - [x] complete incomplete/refusal/max-token stop reason mapping
- [x] Add integration tests using fake HTTP/SSE servers.

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
- [x] Implement SQLite session store:
  - [x] session records
  - [x] stored messages with monotonic sequence
  - [x] task persistence
  - [x] task dependency edges
  - [x] invoked-skill persistence
  - [x] close/reopen behavior
- [x] Add persistence/resume tests matching TypeScript behavior.
- [x] Add migration-safe schema initialization.

## Permissions

- [x] Match TypeScript rule semantics exactly:
  - [x] default, `acceptEdits`, and `yolo` mode defaults
  - [x] basic tool rules
  - [x] basic path rules
  - [x] bash command rules
  - [x] named tool argument rules
  - [x] complete tool argument rules
  - [x] rule ordering and precedence
- [x] Add input rewriting tests for `CanUseTool`.
- [x] Add validation for invalid permission callback responses.
- [x] Emit `permission_request` events before callback execution.
- [x] Add permission-request event batching before callback execution.

## MCP

- [x] Implement MCP server config types:
  - [x] stdio server config
  - [x] HTTP server config
- [x] Implement MCP client lifecycle:
  - [x] connect on first `Agent.Session`
  - [x] register MCP tools as `mcp__server__tool`
  - [x] close child/server connections on `Agent.Close`
  - [x] retry connection after failed first attempt
- [x] Implement MCP tool result conversion.
- [x] Implement MCP naming helpers and collision handling.
- [x] Add MCP end-to-end test with an echo server.

## Skills

- [x] Implement `.skawld/skills/<name>/SKILL.md` loader.
- [x] Parse frontmatter fields:
  - [x] `name`
  - [x] `description`
  - [x] `when_to_use`
  - [x] `argument_hint`
  - [x] `allowed_tools`
  - [x] `model`
- [x] Implement skill listing prompt block.
- [x] Implement Skill tool.
- [x] Implement skill argument substitution.
- [x] Implement one-turn skill overlays:
  - [x] additive allowed tools
  - [x] model override
- [x] Persist invoked skills for resume/compaction replay.
- [x] Emit `skills_loaded`, `skill_invoked`, and `skill_completed` events.

## Subagents

- [x] Implement `.skawld/agents/<name>.md` loader.
- [x] Implement built-in default subagent.
- [x] Implement subagent registry.
- [x] Implement Subagent tool.
- [x] Implement child session runner with filtered tool registry.
- [x] Wrap child events as parent `subagent_event`.
- [x] Support nested subagent events.
- [x] Add subagent end-to-end tests.

## Configuration

- [x] Port config schema types.
- [x] Add config loader equivalent to TypeScript `src/config`.
- [x] Decide Go-specific config file format and precedence.
- [x] Add tests for missing/invalid config.

## Documentation And Examples

- [x] Add examples equivalent to TypeScript:
  - [x] minimal agent
  - [x] MCP agent
  - [x] interactive CLI
- [x] Document provider setup and environment variables.
- [x] Document custom tools in Go.
- [x] Document permission callbacks in Go.
- [x] Document session persistence once SQLite lands.

## Test Parity

- [x] Port core loop tests.
- [x] Port scheduler tests.
- [x] Port provider translation tests.
- [ ] Port permissions tests:
  - [x] permission modes
  - [x] basic tool, path, bash, and named-argument rules
  - [x] callback validation and input rewriting
  - [x] permission-request batching
  - [x] full TypeScript parity fixture coverage
- [ ] Port tool tests:
  - [x] filesystem tools
  - [x] `Glob`
  - [x] `Grep`
  - [x] `Bash`
  - [x] task tools
- [x] Port session store tests:
  - [x] in-memory task storage behavior
  - [x] full persistence/resume parity
- [x] Port MCP tests.
- [x] Port skills tests.
- [x] Port subagent tests.
- [x] Add public API/surface test.
- [x] Add examples build test.
