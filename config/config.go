package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/providers"
	"github.com/skawld/skawld-sdk-go/tools/mcp"
)

type File struct {
	Provider        string              `json:"provider"`
	Model           core.ModelID        `json:"model"`
	CWD             string              `json:"cwd,omitempty"`
	SystemPrompt    string              `json:"system_prompt,omitempty"`
	PermissionMode  core.PermissionMode `json:"permission_mode,omitempty"`
	MaxRetries      int                 `json:"max_retries,omitempty"`
	MaxTurns        int                 `json:"max_turns,omitempty"`
	ToolConcurrency int                 `json:"tool_concurrency,omitempty"`
	Compaction      *CompactionConfig   `json:"compaction,omitempty"`
	SkillsDir       string              `json:"skills_dir,omitempty"`
	AgentsDir       string              `json:"agents_dir,omitempty"`
	MCPServers      []mcp.ServerConfig  `json:"mcp_servers,omitempty"`
	OpenAI          ProviderConfig      `json:"openai,omitempty"`
	Anthropic       ProviderConfig      `json:"anthropic,omitempty"`
}

type ProviderConfig struct {
	APIKey         string            `json:"api_key,omitempty"`
	BaseURL        string            `json:"base_url,omitempty"`
	DefaultHeaders map[string]string `json:"default_headers,omitempty"`
}

type CompactionConfig struct {
	Disabled  bool    `json:"disabled,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}

type LoadOptions struct {
	Path string
	CWD  string
}

type AgentOptions struct {
	Provider            core.Provider
	Model               core.ModelID
	CWD                 string
	SystemPrompt        string
	PermissionMode      core.PermissionMode
	MaxRetries          int
	MaxTurns            int
	ToolConcurrency     int
	CompactionThreshold float64
	DisableCompaction   bool
	MCPServers          []mcp.ServerConfig
	SkillsDir           string
	AgentsDir           string
}

func Load(opts LoadOptions) (File, string, error) {
	path := opts.Path
	if path == "" {
		cwd := opts.CWD
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return File{}, "", fmt.Errorf("get working directory for config discovery: %w", err)
			}
		}
		found, ok, err := findConfig(cwd)
		if err != nil || !ok {
			if err != nil {
				return File{}, "", fmt.Errorf("find config from %s: %w", cwd, err)
			}
			return File{}, "", nil
		}
		path = found
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, "", fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg File
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return File{}, "", fmt.Errorf("invalid config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return File{}, "", fmt.Errorf("validate config %s: %w", path, err)
	}
	return cfg, path, nil
}

func LoadAgentOptions(ctx context.Context, opts LoadOptions) (AgentOptions, string, error) {
	cfg, path, err := Load(opts)
	if err != nil {
		return AgentOptions{}, "", err
	}
	agentOpts, err := cfg.AgentOptions(ctx)
	if err != nil {
		return AgentOptions{}, "", fmt.Errorf("build agent options from config %s: %w", path, err)
	}
	return agentOpts, path, nil
}

func (c File) Validate() error {
	if strings.TrimSpace(c.Provider) == "" {
		return core.NewConfigError("config provider is required")
	}
	switch c.Provider {
	case "openai-responses", "openai-chat", "anthropic":
	default:
		return core.NewConfigError(fmt.Sprintf("unsupported provider %q", c.Provider))
	}
	if c.Model == "" {
		return core.NewConfigError("config model is required")
	}
	for _, server := range c.MCPServers {
		if err := server.Validate(); err != nil {
			return fmt.Errorf("config mcp server %q: %w", server.Name, err)
		}
	}
	return nil
}

func (c File) AgentOptions(ctx context.Context) (AgentOptions, error) {
	if err := c.Validate(); err != nil {
		return AgentOptions{}, fmt.Errorf("validate config before building agent options: %w", err)
	}
	provider, err := c.provider()
	if err != nil {
		return AgentOptions{}, fmt.Errorf("build provider from config: %w", err)
	}
	mode := c.PermissionMode
	if mode == "" {
		mode = core.PermissionModeDefault
	}
	disableCompaction := false
	threshold := 0.0
	if c.Compaction != nil {
		disableCompaction = c.Compaction.Disabled
		threshold = c.Compaction.Threshold
	}
	_ = ctx
	return AgentOptions{
		Provider:            provider,
		Model:               c.Model,
		CWD:                 c.CWD,
		SystemPrompt:        c.SystemPrompt,
		PermissionMode:      mode,
		MaxRetries:          c.MaxRetries,
		MaxTurns:            c.MaxTurns,
		ToolConcurrency:     c.ToolConcurrency,
		CompactionThreshold: threshold,
		DisableCompaction:   disableCompaction,
		MCPServers:          append([]mcp.ServerConfig(nil), c.MCPServers...),
		SkillsDir:           c.SkillsDir,
		AgentsDir:           c.AgentsDir,
	}, nil
}

func (c File) provider() (core.Provider, error) {
	switch c.Provider {
	case "openai-responses":
		return providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{
			APIKey:         c.OpenAI.APIKey,
			BaseURL:        c.OpenAI.BaseURL,
			DefaultHeaders: c.OpenAI.DefaultHeaders,
		}), nil
	case "openai-chat":
		return providers.NewOpenAIChatCompletionsProvider(providers.OpenAIOptions{
			APIKey:         c.OpenAI.APIKey,
			BaseURL:        c.OpenAI.BaseURL,
			DefaultHeaders: c.OpenAI.DefaultHeaders,
		}), nil
	case "anthropic":
		return providers.NewAnthropicProvider(providers.AnthropicOptions{
			APIKey:         c.Anthropic.APIKey,
			BaseURL:        c.Anthropic.BaseURL,
			DefaultHeaders: c.Anthropic.DefaultHeaders,
		}), nil
	default:
		return nil, core.NewConfigError(fmt.Sprintf("unsupported provider %q", c.Provider))
	}
}

func findConfig(cwd string) (string, bool, error) {
	candidates := []string{
		filepath.Join(cwd, "skawld.json"),
		filepath.Join(cwd, ".skawld", "config.json"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true, nil
		} else if !os.IsNotExist(err) {
			return "", false, err
		}
	}
	return "", false, nil
}
