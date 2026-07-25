package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestLoadFindsRootConfigBeforeDotSkawld(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".skawld"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".skawld", "config.json"), []byte(`{"provider":"anthropic","model":"claude"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skawld.json"), []byte(`{"provider":"openai-responses","model":"gpt-5","permission_mode":"yolo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, path, err := Load(LoadOptions{CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "skawld.json" {
		t.Fatalf("unexpected path: %s", path)
	}
	if cfg.Provider != "openai-responses" || cfg.PermissionMode != core.PermissionModeYolo {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadReturnsNoConfigWhenMissing(t *testing.T) {
	cfg, path, err := Load(LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || cfg.Provider != "" {
		t.Fatalf("expected empty config result, path=%q cfg=%+v", path, cfg)
	}
}

func TestLoadValidatesMissingAndInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skawld.json")
	if err := os.WriteFile(path, []byte(`{"model":"gpt-5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(LoadOptions{Path: path}); err == nil {
		t.Fatal("expected missing provider error")
	} else if !errors.Is(err, &core.SkawldError{Kind: core.ErrorConfig}) {
		t.Fatalf("expected typed config error, got %T %[1]v", err)
	}
	if err := os.WriteFile(path, []byte(`{"provider":"unknown","model":"gpt-5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(LoadOptions{Path: path}); err == nil {
		t.Fatal("expected unsupported provider error")
	} else {
		var skerr *core.SkawldError
		if !errors.As(err, &skerr) || skerr.Kind != core.ErrorConfig {
			t.Fatalf("expected typed config error through wrapping, got %T %[1]v", err)
		}
	}
}

func TestAgentOptionsBuildsProviderAndSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skawld.json")
	if err := os.WriteFile(path, []byte(`{
  "provider": "openai-chat",
  "model": "gpt-4o",
  "cwd": "repo",
  "system_prompt": "Be direct.",
  "permission_mode": "acceptEdits",
  "max_retries": 2,
  "max_turns": 10,
  "tool_concurrency": 3,
  "skills_dir": "skills",
  "agents_dir": "agents",
  "compaction": {"disabled": true, "threshold": 0.5},
  "openai": {"api_key": "key", "base_url": "https://example.test/v1"},
  "mcp_servers": [{"name":"srv","http":{"url":"https://example.test/mcp"}}]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, _, err := LoadAgentOptions(context.Background(), LoadOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Provider == nil || opts.Provider.ID() != "openai-chat" {
		t.Fatalf("unexpected provider: %#v", opts.Provider)
	}
	if opts.Model != "gpt-4o" || opts.PermissionMode != core.PermissionModeAcceptEdits || !opts.DisableCompaction {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if opts.CompactionThreshold != 0.5 || len(opts.MCPServers) != 1 || opts.SubagentsDir != "agents" {
		t.Fatalf("unexpected options: %+v", opts)
	}
}
