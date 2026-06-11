package skawld

import (
	"github.com/skawld/skawld-sdk-go/config"
	"github.com/skawld/skawld-sdk-go/sessions"
	"github.com/skawld/skawld-sdk-go/tools"
)

func AgentOptionsFromConfig(opts config.AgentOptions) AgentOptions {
	return AgentOptions{
		Provider:            opts.Provider,
		Model:               opts.Model,
		Tools:               tools.DefaultTools(),
		Permissions:         PermissionOptions{Mode: opts.PermissionMode},
		SessionStore:        sessions.NewInMemoryStore(),
		CWD:                 opts.CWD,
		SystemPrompt:        opts.SystemPrompt,
		MaxRetries:          opts.MaxRetries,
		MaxTurns:            opts.MaxTurns,
		ToolConcurrency:     opts.ToolConcurrency,
		CompactionThreshold: opts.CompactionThreshold,
		DisableCompaction:   opts.DisableCompaction,
		MCPServers:          opts.MCPServers,
		SkillsDir:           opts.SkillsDir,
		SubagentsDir:        opts.SubagentsDir,
	}
}
