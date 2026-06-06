package skawld

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/permissions"
	"github.com/skawld/skawld-sdk-go/sessions"
	"github.com/skawld/skawld-sdk-go/skills"
	"github.com/skawld/skawld-sdk-go/subagents"
	"github.com/skawld/skawld-sdk-go/tools"
	"github.com/skawld/skawld-sdk-go/tools/mcp"
)

type PermissionOptions struct {
	Mode       core.PermissionMode
	Rules      []permissions.Rule
	CanUseTool permissions.CanUseTool
}

type AgentOptions struct {
	Provider               core.Provider
	Model                  core.ModelID
	Tools                  *tools.Registry
	Permissions            PermissionOptions
	SessionStore           core.SessionStore
	CWD                    string
	SystemPrompt           string
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
	AgentsDir              string
	DisableSkills          bool
	DisableSubagents       bool
}

type Agent struct {
	opts       AgentOptions
	perm       *permissions.Engine
	store      core.SessionStore
	system     []core.SystemBlock
	mcp        *mcp.Manager
	skills     *skills.Manager
	skillsTool bool
	subagents  *subagents.Registry
	subTool    bool
	sessionsMu sync.Mutex
}

func NewAgent(opts AgentOptions) (*Agent, error) {
	if opts.Provider == nil {
		return nil, core.NewConfigError("Agent requires a provider")
	}
	if !core.SupportsStreamingProvider(opts.Provider) {
		return nil, core.NewConfigError("Agent provider must implement Stream")
	}
	if opts.Model == "" {
		return nil, core.NewConfigError("Agent requires a model")
	}
	if opts.Tools == nil {
		opts.Tools = tools.DefaultTools()
	}
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
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 5
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
	if opts.AgentsDir == "" {
		opts.AgentsDir = filepath.Join(opts.CWD, ".skawld", "agents")
	}
	a := &Agent{opts: opts, store: opts.SessionStore}
	if len(opts.MCPServers) > 0 {
		a.mcp = mcp.NewManager(opts.MCPServers)
	}
	if !opts.DisableSkills {
		a.skills = skills.NewManager(opts.SkillsDir)
	}
	if !opts.DisableSubagents {
		a.subagents = subagents.NewRegistry(opts.AgentsDir)
	}
	a.perm = permissions.NewEngine(permissions.Options{
		Mode: opts.Permissions.Mode, Rules: opts.Permissions.Rules,
		CanUseTool: opts.Permissions.CanUseTool, ProjectRoot: opts.CWD,
	})
	a.system = buildSystemBlocks(opts.CWD, opts.Permissions.Mode, opts.Tools.Names(), opts.SystemPrompt)
	return a, nil
}

type SessionOptions struct {
	ID   string
	Meta map[string]interface{}
}

func (a *Agent) Session(ctx context.Context, opts SessionOptions) (*Session, error) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if err := a.connectMCP(ctx); err != nil {
		return nil, err
	}
	if err := a.loadSubagents(); err != nil {
		return nil, err
	}
	skillEvents, err := a.loadSkills()
	if err != nil {
		return nil, err
	}
	rec, err := a.store.Create(opts.ID, opts.Meta)
	if err != nil {
		return nil, err
	}
	if loaded, ok, err := a.store.Load(rec.ID); err != nil {
		return nil, err
	} else if ok {
		rec = loaded
	}
	stored, err := a.store.LoadMessages(rec.ID)
	if err != nil {
		return nil, err
	}
	view := make([]core.Message, 0, len(stored))
	for _, sm := range stored {
		view = append(view, sm.Message)
	}
	return newSession(a, rec, view, skillEvents), nil
}

func (a *Agent) loadSubagents() error {
	if a.subagents == nil {
		return nil
	}
	if err := a.subagents.Load(); err != nil {
		return err
	}
	if !a.subTool {
		if _, exists := a.opts.Tools.Get("Subagent"); exists {
			a.subTool = true
		} else {
			if err := a.opts.Tools.Register(subagents.Tool{Registry: a.subagents}); err != nil {
				return err
			}
			a.subTool = true
		}
	}
	a.rebuildSystem()
	return nil
}

func (a *Agent) Close() error {
	var mcpErr error
	if a.mcp != nil {
		mcpErr = a.mcp.Close()
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			return err
		}
	}
	return mcpErr
}

func (a *Agent) Options() AgentOptions { return a.opts }

func (a *Agent) connectMCP(ctx context.Context) error {
	if a.mcp == nil {
		return nil
	}
	discovered, err := a.mcp.Connect(ctx, a.opts.Tools.Names())
	if err != nil {
		return err
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
	wasLoaded := a.skills.Loaded()
	if err := a.skills.Load(); err != nil {
		return nil, err
	}
	defs := a.skills.Definitions()
	if len(defs) == 0 {
		return nil, nil
	}
	if !a.skillsTool {
		if _, exists := a.opts.Tools.Get("Skill"); exists {
			a.skillsTool = true
		} else {
			if err := a.opts.Tools.Register(skills.Tool{Manager: a.skills}); err != nil {
				return nil, err
			}
			a.skillsTool = true
		}
	}
	a.rebuildSystem()
	if wasLoaded {
		return nil, nil
	}
	return []core.Event{{
		Type: core.EventSkillsLoaded,
		Delta: map[string]interface{}{
			"skills": a.skills.Names(),
		},
	}}, nil
}

func (a *Agent) rebuildSystem() {
	a.system = buildSystemBlocks(a.opts.CWD, a.opts.Permissions.Mode, a.opts.Tools.Names(), a.opts.SystemPrompt)
	if a.subagents != nil && a.subagents.Loaded() {
		if listing := subagentListingPrompt(a.subagents.Definitions()); listing != "" {
			a.system = append(a.system, core.SystemBlock{Type: "text", Text: listing, Cacheable: true})
		}
	}
	if a.skills != nil && a.skills.Loaded() {
		if listing := skills.ListingPrompt(a.skills.Definitions()); listing != "" {
			a.system = append(a.system, core.SystemBlock{Type: "text", Text: listing, Cacheable: true})
		}
	}
}
