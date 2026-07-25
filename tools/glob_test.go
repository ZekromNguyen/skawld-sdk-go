package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

var fixtureDir string

func makeCtx(cwd string) core.ToolContext {
	return core.ToolContext{
		CWD:     cwd,
		Context: context.Background(),
	}
}

func setupFixture(t *testing.T) {
	var err error
	fixtureDir, err = os.MkdirTemp("", "skawld-glob-*")
	if err != nil {
		t.Fatal(err)
	}

	dirs := []string{"a", "b", "c", "c/hidden-sub"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(fixtureDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		"a/foo.ts":             "export const foo = 1;",
		"a/bar.ts":             "export const bar = 2;",
		"b/baz.js":             "const baz = 3;",
		"c/hidden-sub/deep.ts": "export const deep = 4;",
		".hidden.ts":           "// hidden",
		"README.md":            "# readme",
	}

	tm := time.Now().Add(-10 * time.Second)
	// Create map with fixed order to stagger correctly
	order := []string{
		"a/foo.ts",
		"a/bar.ts",
		"b/baz.js",
		"c/hidden-sub/deep.ts",
		".hidden.ts",
		"README.md",
	}

	for _, rel := range order {
		content := files[rel]
		abs := filepath.Join(fixtureDir, filepath.FromSlash(rel))
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, tm, tm); err != nil {
			t.Fatal(err)
		}
		tm = tm.Add(1 * time.Second)
	}
}

func cleanupFixture() {
	if fixtureDir != "" {
		os.RemoveAll(fixtureDir)
	}
}

func TestGlobToolMatchesRecursively(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	input, err := tool.Validate(map[string]interface{}{"pattern": "**/*.ts"})
	if err != nil {
		t.Fatal(err)
	}

	ctx := makeCtx(fixtureDir)
	res, err := tool.Execute(input, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	content := res.Content.(string)
	lines := strings.Split(content, "\n")
	hasHidden := false
	tsCount := 0
	for _, l := range lines {
		if l == "" {
			continue
		}
		if strings.Contains(l, ".hidden.ts") {
			hasHidden = true
		}
		if strings.HasSuffix(l, ".ts") {
			tsCount++
		}
	}

	if hasHidden {
		t.Errorf("expected .hidden.ts to be skipped")
	}
	if tsCount < 3 {
		t.Errorf("expected at least 3 ts files, got %d", tsCount)
	}
}

func TestGlobToolNoMatches(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	input, _ := tool.Validate(map[string]interface{}{"pattern": "**/*.xyz"})
	ctx := makeCtx(fixtureDir)
	res, _ := tool.Execute(input, ctx)

	if res.Content != "No matches found." {
		t.Errorf("expected 'No matches found.', got: %v", res.Content)
	}
}

func TestGlobToolExplicitPath(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	input, _ := tool.Validate(map[string]interface{}{
		"pattern": "**/*.ts",
		"path":    filepath.Join(fixtureDir, "a"),
	})
	ctx := makeCtx(fixtureDir)
	res, _ := tool.Execute(input, ctx)

	lines := strings.Split(res.Content.(string), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 files, got %d", len(lines))
	}
	for _, l := range lines {
		if !strings.HasSuffix(l, ".ts") {
			t.Errorf("expected only ts files: %s", l)
		}
		if strings.Contains(l, "deep") {
			t.Errorf("expected deep to be excluded")
		}
	}
}

func TestGlobToolSortsByMtime(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	input, _ := tool.Validate(map[string]interface{}{"pattern": "**/*.ts"})
	ctx := makeCtx(fixtureDir)
	res, _ := tool.Execute(input, ctx)

	lines := strings.Split(res.Content.(string), "\n")
	var tsLines []string
	for _, l := range lines {
		if strings.HasSuffix(l, ".ts") {
			tsLines = append(tsLines, l)
		}
	}

	if len(tsLines) < 1 || !strings.Contains(tsLines[0], "deep") {
		t.Errorf("expected deep.ts to be first, got %v", tsLines)
	}
}

func TestGlobToolMatchExactPattern(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	input, _ := tool.Validate(map[string]interface{}{"pattern": "**/*.md"})
	ctx := makeCtx(fixtureDir)
	res, _ := tool.Execute(input, ctx)

	if !strings.Contains(res.Content.(string), "README.md") {
		t.Errorf("expected README.md, got %v", res.Content)
	}
}

func TestGlobToolFallbackEquivalence(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	input, _ := tool.Validate(map[string]interface{}{"pattern": "**/*.ts"})
	ctx := makeCtx(fixtureDir)

	// fast path (ripgrep) if available
	res1, _ := tool.Execute(input, ctx)

	// fallback path
	_, hasRg := executable("rg")
	if hasRg {
		// Temporary move rg and execute to force fallback
		os.Setenv("PATH", "") // Try to block shell from finding rg, though exec.LookPath might ignore if cached
	}

	files, err := globFallback(ctx, fixtureDir, "**/*.ts")
	if err != nil {
		t.Fatal(err)
	}

	res2, _ := formatGlobResults(files, fixtureDir)

	if res1.Content != res2.Content {
		t.Errorf("Fallback mismatch!\nexpected: %v\ngot: %v", res1.Content, res2.Content)
	}
}
