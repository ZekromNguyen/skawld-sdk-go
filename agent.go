package skawld

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/permissions"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
	"github.com/ZekromNguyen/skawld-sdk-go/skills"
	"github.com/ZekromNguyen/skawld-sdk-go/subagents"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
	"github.com/ZekromNguyen/skawld-sdk-go/tools/mcp"
)

type PermissionOptions struct {
	Mode       core.PermissionMode
	Rules      []permissions.Rule
	CanUseTool permissions.CanUseTool
}

// ProblemSolvingOptions enables lightweight orchestration hints that help a
// coding agent choose better next actions without changing provider APIs.
type ProblemSolvingOptions struct {
	Enabled                  bool
	AutoRepoMap              bool
	RequirePlanBeforeWrite   bool
	AutoVerify               bool
	MaxConsecutiveToolErrors int
}

type ProviderRetryPolicy struct {
	// MaxRetries is additional attempts after the initial request. A value of
	// zero explicitly disables retries when ProviderRetry is non-nil.
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// RuntimeLimits bounds one agent run and the data retained by its session.
// Zero values preserve the development-mode behavior. ProductionOptions
// requires every limit to be positive.
type RuntimeLimits struct {
	MaxRunDuration           time.Duration
	MaxToolCalls             int
	MaxToolResultBytes       int
	MaxProviderResponseBytes int
	MaxProviderEvents        int
	MaxOutputTokensPerTurn   int
	MaxSessionBytes          int
	MaxTotalTokens           int
}

// ProductionOptions turns on fail-closed runtime validation. Policy is a hard
// authorization boundary and cannot be bypassed by interactive permissions.
// AuditOutbox is required so consequential decisions survive sink outages.
type ProductionOptions struct {
	Policy      policy.Evaluator
	Audit       audit.Sink
	AuditOutbox audit.Outbox
	Limits      RuntimeLimits
	// AllowSkills opts into repository-local instruction overlays. Production
	// disables skill discovery by default because skill bodies become trusted
	// instructions. Subagents and MCP tools remain unavailable from the
	// production agent boundary; execute integrations through workflows.
	AllowSkills bool
}

// AgentOptions configures an Agent. NewAgent clones the supplied Tools
// registry before adding runtime tools, so callers keep ownership of their
// registry after construction.
type AgentOptions struct {
	Provider        core.Provider
	ProviderFactory core.ProviderFactory
	Model           core.ModelID
	Tools           *tools.Registry
	// CloseTools transfers registered tool resource lifecycle to Agent.Close.
	// Tools constructed internally are always owned by the Agent.
	CloseTools       bool
	Permissions      PermissionOptions
	SessionStore     core.SessionStore
	CWD              string
	FilesystemPolicy tools.FilesystemPolicy
	Principal        core.Principal
	ToolProfile      tools.Profile
	Logger           *slog.Logger
	Observer         core.Observer
	SystemPrompt     string
	ProblemSolving   ProblemSolvingOptions
	ProviderRetry    *ProviderRetryPolicy
	// MaxRetries is retained for compatibility. Prefer ProviderRetry, whose
	// non-nil zero value can explicitly disable retries.
	MaxRetries             int
	MaxOutputTokens        *int
	IncludePartialMessages bool
	MaxTurns               int
	ToolConcurrency        int
	CompactionStrategy     CompactionStrategy
	CompactionThreshold    float64
	DisableCompaction      bool
	MCPServers             []mcp.ServerConfig
	SkillsDir              string
	SubagentsDir           string
	DisableSkills          bool
	DisableSubagents       bool
	// Production enables fail-closed identity, authorization, audit, storage,
	// and resource-limit validation. Development defaults remain unchanged when
	// this field is nil.
	Production *ProductionOptions
}

// Agent owns shared SDK runtime resources and can create multiple sessions
// concurrently. Close should be called when MCP or store resources are no
// longer needed.
type Agent struct {
	opts            AgentOptions
	perm            *permissions.Engine
	policy          policy.Evaluator
	audit           audit.Sink
	store           core.SessionStore
	system          []core.SystemBlock
	systemMu        sync.RWMutex
	staticMu        sync.RWMutex
	toolCache       []core.ToolSchema
	staticLen       int
	mcp             *mcp.Manager
	skills          *skills.Manager
	subagents       *subagents.Registry
	mcpMu           sync.Mutex
	skillsMu        sync.Mutex
	subMu           sync.Mutex
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	lifecycleMu     sync.Mutex
	runWG           sync.WaitGroup
	closed          bool
	closeOnce       sync.Once
	closeErr        error
	ownsTools       bool
}

func NewAgent(opts AgentOptions) (*Agent, error) {
	if err := validateProductionAgentOptions(opts); err != nil {
		return nil, err
	}
	if opts.Production != nil {
		if !opts.Production.AllowSkills {
			opts.DisableSkills = true
		}
		opts.DisableSubagents = true
	}
	if opts.Provider == nil && opts.ProviderFactory != nil {
		opts.Provider = opts.ProviderFactory.NewProvider()
	}
	if opts.Provider == nil {
		return nil, core.NewConfigError("Agent requires a provider")
	}
	if !core.SupportsStreamingProvider(opts.Provider) {
		return nil, core.NewConfigError("Agent provider must implement Stream")
	}
	if opts.Model == "" {
		return nil, core.NewConfigError("Agent requires a model")
	}
	ownsTools := opts.Tools == nil || opts.CloseTools
	if opts.Tools == nil {
		if opts.ToolProfile == "" {
			opts.ToolProfile = tools.ProfileCoding
		}
		var err error
		opts.Tools, err = tools.ToolsForProfile(opts.ToolProfile)
		if err != nil {
			return nil, core.NewConfigError(err.Error())
		}
	} else {
		opts.Tools = opts.Tools.Clone()
	}
	opts.ProblemSolving = normalizeProblemSolvingOptions(opts.ProblemSolving)
	if opts.CWD == "" {
		cwd, _ := os.Getwd()
		opts.CWD = cwd
	}
	if opts.Permissions.Mode == "" {
		opts.Permissions.Mode = core.PermissionModeDefault
	}
	if opts.SessionStore == nil {
		opts.SessionStore = sessions.NewInMemoryStore()
	}
	if opts.ProviderRetry == nil {
		if opts.MaxRetries == 0 {
			opts.MaxRetries = 5
		}
		opts.ProviderRetry = &ProviderRetryPolicy{
			MaxRetries: opts.MaxRetries, InitialBackoff: 250 * time.Millisecond, MaxBackoff: 5 * time.Second,
		}
	} else {
		if opts.ProviderRetry.MaxRetries < 0 {
			return nil, core.NewConfigError("provider retry max retries must not be negative")
		}
		retry := *opts.ProviderRetry
		if retry.InitialBackoff <= 0 {
			retry.InitialBackoff = 250 * time.Millisecond
		}
		if retry.MaxBackoff <= 0 {
			retry.MaxBackoff = 5 * time.Second
		}
		if retry.MaxBackoff < retry.InitialBackoff {
			return nil, core.NewConfigError("provider retry max backoff must be at least initial backoff")
		}
		opts.ProviderRetry = &retry
		opts.MaxRetries = retry.MaxRetries
	}
	if opts.MaxTurns == 0 {
		opts.MaxTurns = 1000
	}
	if opts.ToolConcurrency <= 0 {
		opts.ToolConcurrency = 8
	}
	if opts.CompactionThreshold <= 0 {
		opts.CompactionThreshold = defaultCompactionThreshold
	}
	if !opts.DisableCompaction && opts.CompactionStrategy == nil {
		opts.CompactionStrategy = DefaultCompactionStrategy()
	}
	if opts.SkillsDir == "" {
		opts.SkillsDir = filepath.Join(opts.CWD, ".skawld", "skills")
	}
	if opts.SubagentsDir == "" {
		opts.SubagentsDir = filepath.Join(opts.CWD, ".skawld", "agents")
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	a := &Agent{opts: opts, lifecycleCtx: lifecycleCtx, lifecycleCancel: lifecycleCancel, ownsTools: ownsTools}
	if opts.Production != nil {
		dispatcher, err := audit.NewDispatcher(
			opts.Production.AuditOutbox, opts.Production.Audit,
		)
		if err != nil {
			lifecycleCancel()
			return nil, err
		}
		a.policy = opts.Production.Policy
		a.audit = dispatcher
	}
	a.store = &observedSessionStore{inner: opts.SessionStore, agent: a}
	if len(opts.MCPServers) > 0 {
		a.mcp = mcp.NewManager(opts.MCPServers)
	}
	if !opts.DisableSkills {
		a.skills = skills.NewManager(opts.SkillsDir)
	}
	if !opts.DisableSubagents {
		a.subagents = subagents.NewRegistry(opts.SubagentsDir)
	}
	a.perm = permissions.NewEngine(permissions.Options{
		Mode: opts.Permissions.Mode, Rules: opts.Permissions.Rules,
		CanUseTool: opts.Permissions.CanUseTool, ProjectRoot: opts.CWD, Observer: a,
	})
	a.system = buildSystemBlocks(opts.CWD, opts.Permissions.Mode, opts.Tools.Names(), opts.SystemPrompt)
	a.refreshStaticProviderInputs(a.system)
	return a, nil
}

func normalizeProblemSolvingOptions(opts ProblemSolvingOptions) ProblemSolvingOptions {
	if !opts.Enabled && !opts.AutoRepoMap && !opts.RequirePlanBeforeWrite && !opts.AutoVerify && opts.MaxConsecutiveToolErrors == 0 {
		opts.Enabled = true
		opts.AutoRepoMap = true
		opts.RequirePlanBeforeWrite = true
		opts.AutoVerify = true
		opts.MaxConsecutiveToolErrors = 2
		return opts
	}
	if opts.MaxConsecutiveToolErrors <= 0 {
		opts.MaxConsecutiveToolErrors = 2
	}
	return opts
}

type SessionOptions struct {
	ID        string
	Meta      map[string]interface{}
	Principal core.Principal
}

func (a *Agent) Session(ctx context.Context, opts SessionOptions) (*Session, error) {
	a.lifecycleMu.Lock()
	closed := a.closed
	a.lifecycleMu.Unlock()
	if closed {
		return nil, core.NewConfigError("agent is closed")
	}
	principal := opts.Principal
	if !principal.Valid() {
		principal = a.opts.Principal
	}
	if a.production() != nil {
		authenticated, exists := core.PrincipalFromContext(ctx)
		if !exists || !authenticated.Authenticated() {
			return nil, core.NewPermissionError(
				"production session requires an authenticated context identity",
			)
		}
		if authenticated.TenantID != a.opts.Principal.TenantID ||
			authenticated.ActorID != a.opts.Principal.ActorID {
			return nil, core.NewPermissionError(
				"production session identity does not match the agent identity",
			)
		}
		if principal.Authenticated() &&
			(principal.TenantID != authenticated.TenantID ||
				principal.ActorID != authenticated.ActorID) {
			return nil, core.NewPermissionError(
				"production session options cannot replace authenticated identity",
			)
		}
		principal = authenticated
	}
	meta, ok := core.BindPrincipalToSessionMeta(opts.Meta, principal)
	if !ok {
		return nil, core.NewConfigError("session metadata conflicts with authenticated principal")
	}
	if principal.Valid() {
		ctx = core.WithPrincipal(ctx, principal)
	}
	rec, err := a.store.Create(ctx, opts.ID, meta)
	if err != nil {
		return nil, err
	}
	if !a.canAccessSession(principal, rec.Meta) {
		return nil, core.NewPermissionError("session belongs to another tenant")
	}
	if loaded, ok, err := a.store.Load(ctx, rec.ID); err != nil {
		return nil, err
	} else if ok {
		if !a.canAccessSession(principal, loaded.Meta) {
			return nil, core.NewPermissionError("session belongs to another tenant")
		}
		rec = loaded
	}
	stored, err := a.store.LoadMessages(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	view := make([]core.Message, 0, len(stored))
	for _, sm := range stored {
		view = append(view, sm.Message)
	}
	return newSession(a, rec, view, nil, principal), nil
}

func (a *Agent) canAccessSession(
	principal core.Principal,
	meta map[string]interface{},
) bool {
	if a.production() == nil {
		return core.CanAccessSession(principal, meta)
	}
	owner := core.PrincipalFromSessionMeta(meta)
	return principal.Authenticated() && owner.Authenticated() &&
		principal.TenantID == owner.TenantID &&
		principal.ActorID == owner.ActorID
}

func (a *Agent) loadRuntime(ctx context.Context) ([]core.Event, error) {
	runtimeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		events []core.Event
		err    error
	}
	results := make(chan result, 3)
	go func() {
		results <- result{err: a.connectMCP(runtimeCtx)}
	}()
	go func() {
		results <- result{err: a.loadSubagents()}
	}()
	go func() {
		events, err := a.loadSkills()
		results <- result{events: events, err: err}
	}()

	var events []core.Event
	var firstErr error
	for i := 0; i < 3; i++ {
		res := <-results
		if res.err != nil && firstErr == nil {
			firstErr = res.err
			cancel()
		}
		if firstErr == nil {
			events = append(events, res.events...)
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return events, nil
}

func (a *Agent) loadSubagents() error {
	if a.subagents == nil {
		return nil
	}
	a.subMu.Lock()
	if err := a.subagents.Load(); err != nil {
		a.subMu.Unlock()
		return err
	}
	a.subMu.Unlock()
	a.rebuildSystem()
	return nil
}

func (a *Agent) Close() error {
	a.closeOnce.Do(func() {
		a.lifecycleMu.Lock()
		a.closed = true
		a.lifecycleCancel()
		a.lifecycleMu.Unlock()
		a.runWG.Wait()
		var errs []error
		if a.mcp != nil {
			if err := a.mcp.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if a.ownsTools && a.opts.Tools != nil {
			if err := a.opts.Tools.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if a.store != nil {
			if err := a.store.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		a.closeErr = errors.Join(errs...)
	})
	return a.closeErr
}

func (a *Agent) beginRun() bool {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.closed {
		return false
	}
	a.runWG.Add(1)
	return true
}

func (a *Agent) endRun() {
	a.runWG.Done()
}

func (a *Agent) Options() AgentOptions {
	opts := a.opts
	opts.Tools = a.opts.Tools.Clone()
	return opts
}

// Store returns the underlying session store so callers can list, delete, and
// query sessions.
func (a *Agent) Store() core.SessionStore { return a.store }

func (a *Agent) connectMCP(ctx context.Context) error {
	if a.mcp == nil {
		return nil
	}
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	start := time.Now()
	discovered, err := a.mcp.Connect(ctx, a.opts.Tools.Names())
	a.observe(ctx, core.Observation{Type: core.ObservationMCPCall, Operation: "connect", DurationMS: time.Since(start).Milliseconds(), Error: err})
	if err != nil {
		return fmt.Errorf("connect mcp servers: %w", err)
	}
	if a.production() != nil && len(discovered) > 0 {
		_ = a.mcp.Close()
		return core.NewConfigError(
			"production agent rejected dynamically discovered MCP tools",
		)
	}
	for _, tool := range discovered {
		if err := a.opts.Tools.Register(tool); err != nil {
			_ = a.mcp.Close()
			return err
		}
	}
	if len(discovered) > 0 {
		a.rebuildSystem()
	}
	return nil
}

func (a *Agent) loadSkills() ([]core.Event, error) {
	if a.skills == nil {
		return nil, nil
	}
	a.skillsMu.Lock()
	wasLoaded := a.skills.Loaded()
	if err := a.skills.Load(); err != nil {
		a.skillsMu.Unlock()
		return nil, err
	}
	defs := a.skills.Definitions()
	if len(defs) == 0 {
		a.skillsMu.Unlock()
		return nil, nil
	}
	if _, exists := a.opts.Tools.Get("Skill"); !exists {
		tool := skills.Tool{Manager: a.skills}
		if a.production() != nil {
			if err := validateProductionAgentTool(tool); err != nil {
				a.skillsMu.Unlock()
				return nil, err
			}
		}
		if err := a.opts.Tools.Register(tool); err != nil {
			a.skillsMu.Unlock()
			return nil, err
		}
	}
	names := a.skills.Names()
	a.skillsMu.Unlock()
	a.rebuildSystem()
	if wasLoaded {
		return nil, nil
	}
	return []core.Event{{
		Type: core.EventSkillsLoaded,
		Delta: map[string]interface{}{
			"skills": names,
		},
	}}, nil
}

func (a *Agent) rebuildSystem() {
	next := buildSystemBlocks(a.opts.CWD, a.opts.Permissions.Mode, a.opts.Tools.Names(), a.opts.SystemPrompt)
	a.subMu.Lock()
	if a.subagents != nil && a.subagents.Loaded() {
		if listing := subagentListingPrompt(a.subagents.Definitions()); listing != "" {
			next = append(next, core.SystemBlock{Type: "text", Text: listing, Cacheable: true})
		}
	}
	a.subMu.Unlock()
	a.skillsMu.Lock()
	if a.skills != nil && a.skills.Loaded() {
		if listing := skills.ListingPrompt(a.skills.Definitions()); listing != "" {
			next = append(next, core.SystemBlock{Type: "text", Text: listing, Cacheable: true})
		}
	}
	a.skillsMu.Unlock()
	a.systemMu.Lock()
	a.system = next
	a.systemMu.Unlock()
	a.refreshStaticProviderInputs(next)
}

func (a *Agent) systemBlocks() []core.SystemBlock {
	a.systemMu.RLock()
	defer a.systemMu.RUnlock()
	return slices.Clone(a.system)
}

func (a *Agent) refreshStaticProviderInputs(system []core.SystemBlock) {
	tools := a.opts.Tools.Schemas()
	a.staticMu.Lock()
	a.toolCache = slices.Clone(tools)
	a.staticLen = estimateStaticProviderChars(system, tools)
	a.staticMu.Unlock()
}

func (a *Agent) toolSchemas() []core.ToolSchema {
	a.staticMu.RLock()
	defer a.staticMu.RUnlock()
	return slices.Clone(a.toolCache)
}

func (a *Agent) staticProviderChars() int {
	a.staticMu.RLock()
	defer a.staticMu.RUnlock()
	return a.staticLen
}

func (a *Agent) providerForSubagent() core.Provider {
	if a.opts.ProviderFactory != nil {
		provider := a.opts.ProviderFactory.NewProvider()
		if provider != nil {
			return provider
		}
	}
	return a.opts.Provider
}
