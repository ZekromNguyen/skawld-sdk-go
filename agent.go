package skawld

import (
	"context"
	"os"
	"sync"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/permissions"
	"github.com/skawld/skawld-sdk-go/sessions"
	"github.com/skawld/skawld-sdk-go/tools"
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
}

type Agent struct {
	opts       AgentOptions
	perm       *permissions.Engine
	store      core.SessionStore
	system     []core.SystemBlock
	sessionsMu sync.Mutex
}

func NewAgent(opts AgentOptions) (*Agent, error) {
	if opts.Provider == nil {
		return nil, core.NewConfigError("Agent requires a provider")
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
	a := &Agent{opts: opts, store: opts.SessionStore}
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
	rec, err := a.store.Create(opts.ID, opts.Meta)
	if err != nil {
		return nil, err
	}
	stored, err := a.store.LoadMessages(rec.ID)
	if err != nil {
		return nil, err
	}
	view := make([]core.Message, 0, len(stored))
	for _, sm := range stored {
		view = append(view, sm.Message)
	}
	return newSession(a, rec, view), nil
}

func (a *Agent) Close() error {
	if a.store != nil {
		return a.store.Close()
	}
	return nil
}

func (a *Agent) Options() AgentOptions { return a.opts }
