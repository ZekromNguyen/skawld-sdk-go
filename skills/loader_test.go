package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileParsesFrontmatterFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(`---
name: review
description: Review code
when_to_use: Before merging
argument_hint: files
allowed_tools: [Read, Grep]
model: expert-model
---
Use $ARGUMENTS carefully.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "review" || def.Description != "Review code" || def.WhenToUse != "Before merging" || def.ArgumentHint != "files" {
		t.Fatalf("unexpected definition: %+v", def)
	}
	if len(def.AllowedTools) != 2 || def.AllowedTools[0] != "Read" || def.AllowedTools[1] != "Grep" {
		t.Fatalf("unexpected allowed tools: %+v", def.AllowedTools)
	}
	if def.Model != "expert-model" {
		t.Fatalf("unexpected model: %s", def.Model)
	}
	if got := Substitute(def, "main.go"); got != "Use main.go carefully." {
		t.Fatalf("unexpected substitution: %q", got)
	}
}

func TestManagerLoadsSkillsAndListingPrompt(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "commit", `---
description: Commit helper
allowed_tools:
  - Bash
---
Body`)
	writeSkill(t, root, "review", `---
name: review
description: Review helper
---
Body`)
	manager := NewManager(root)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if got := manager.Names(); len(got) != 2 || got[0] != "commit" || got[1] != "review" {
		t.Fatalf("unexpected names: %+v", got)
	}
	listing := ListingPrompt(manager.Definitions())
	if !strings.Contains(listing, "commit") || !strings.Contains(listing, "Review helper") || !strings.Contains(listing, "Allowed tools: Bash") {
		t.Fatalf("unexpected listing: %s", listing)
	}
}

func TestSubstituteAppendsArgumentsWhenNoPlaceholder(t *testing.T) {
	def := Definition{Name: "x", Body: "Body only"}
	got := Substitute(def, "arg value")
	if !strings.Contains(got, "Body only") || !strings.Contains(got, "Arguments:\narg value") {
		t.Fatalf("unexpected substitution: %s", got)
	}
}

func TestSplitArgumentsShellStyle(t *testing.T) {
	got, err := SplitArguments(`main.go "two words" 'single quoted' escaped\ space`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"main.go", "two words", "single quoted", "escaped space"}
	if len(got) != len(want) {
		t.Fatalf("unexpected arguments: got=%+v want=%+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arguments: got=%+v want=%+v", got, want)
		}
	}
}

func TestSubstituteArgumentsJSONUsesShellSplit(t *testing.T) {
	def := Definition{Name: "x", Body: "Args: {{arguments_json}}"}
	got := Substitute(def, `main.go "two words"`)
	if got != `Args: ["main.go","two words"]` {
		t.Fatalf("unexpected substitution: %s", got)
	}
}

func TestSplitArgumentsRejectsUnterminatedQuote(t *testing.T) {
	if _, err := SplitArguments(`"unterminated`); err == nil {
		t.Fatal("expected unterminated quote error")
	}
}

func writeSkill(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
