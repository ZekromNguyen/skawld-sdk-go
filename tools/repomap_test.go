package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/sessions"
)

func TestRepoMapToolSummarizesGoRepository(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/repo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "alpha", "alpha.go"), []byte("package alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "alpha", "alpha_test.go"), []byte("package alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := RepoMapTool{}
	input, err := tool.Validate(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(input, core.ToolContext{
		Context:         context.Background(),
		CWD:             dir,
		FileReadTracker: NewFileReadTracker(),
		SessionID:       "s",
		RunID:           "r",
		SessionStore:    sessions.NewInMemoryStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("repo map failed: %v", res.Content)
	}
	content := res.Content.(string)
	if !strings.Contains(content, "module: example.test/repo") {
		t.Fatalf("expected module in repo map, got:\n%s", content)
	}
	if !strings.Contains(content, "pkg/alpha") || !strings.Contains(content, "go test ./...") {
		t.Fatalf("expected package and verification commands, got:\n%s", content)
	}
}
