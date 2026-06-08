package subagents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileParsesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	if err := os.WriteFile(path, []byte(`---
name: review
description: Review code
system_prompt: Check the code carefully.
tools: [Read, Grep]
model: specialist-model
---
Fallback body
`), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "review" || def.Description != "Review code" || def.SystemPrompt != "Check the code carefully." {
		t.Fatalf("unexpected definition: %+v", def)
	}
	if len(def.Tools) != 2 || def.Tools[0] != "Read" || def.Tools[1] != "Grep" {
		t.Fatalf("unexpected tools: %+v", def.Tools)
	}
	if def.Model != "specialist-model" {
		t.Fatalf("unexpected model: %s", def.Model)
	}
}

func TestRegistryLoadsDefaultAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "research.md"), []byte(`---
description: Research helper
tools:
  - Read
---
Research instructions
`), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("default"); !ok {
		t.Fatal("expected built-in default subagent")
	}
	def, ok := reg.Get("research")
	if !ok {
		t.Fatal("expected research subagent")
	}
	if def.SystemPrompt != "Research instructions" {
		t.Fatalf("unexpected system prompt: %q", def.SystemPrompt)
	}
}
