package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepToolContent(t *testing.T) {
	setupGrepFixture(t)
	defer cleanupGrepFixture()

	fb := getFallbackLines(t, "TODO", map[string]interface{}{"output_mode": "content", "-n": true})
	var actual []string
	for _, l := range fb {
		if strings.HasPrefix(l, "src/foo.ts:2:") || strings.HasPrefix(l, "src/bar.ts:3:") || strings.HasPrefix(l, "docs/readme.md:3:") {
			actual = append(actual, l)
		}
	}
	if len(actual) != 3 {
		t.Errorf("Expected 3 TODOs, found %d: %v", len(actual), fb)
	}
}

func TestGrepFallbackStreamingHonorsHeadLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.txt")
	content := strings.Repeat("TODO match\n", 20)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := GrepTool{}
	input, err := tool.Validate(map[string]interface{}{"pattern": "TODO", "output_mode": "content", "-n": true, "head_limit": 3})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runGrepFallback(makeCtx(dir), input, dir)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected fallback to stop at head_limit, got %d lines:\n%s", len(lines), out)
	}
}
