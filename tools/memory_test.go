package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/sessions"
)

func TestMemoryWriteReadAndSearch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SKAWLD_MEMORY_DIR", dir)
	ctx := core.ToolContext{Context: context.Background(), CWD: dir, SessionStore: sessions.NewInMemoryStore()}

	write := MemoryWriteTool{}
	writeInput, err := write.Validate(map[string]interface{}{
		"name":        "project",
		"description": "Project facts",
		"content":     "Skawld supports persistent project memory.\n",
		"metadata":    map[string]interface{}{"category": "project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := write.Execute(writeInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("write failed: %v", res.Content)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "project.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "name: \"project\"") || !strings.Contains(string(raw), "category: \"project\"") {
		t.Fatalf("expected frontmatter, got:\n%s", raw)
	}

	read := MemoryReadTool{}
	readInput, err := read.Validate(map[string]interface{}{"name": "project"})
	if err != nil {
		t.Fatal(err)
	}
	res, err = read.Execute(readInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(fmt.Sprint(res.Content), "persistent project memory") {
		t.Fatalf("unexpected read result: %+v", res)
	}

	search := MemorySearchTool{}
	searchInput, err := search.Validate(map[string]interface{}{"query": "persistent", "limit": 5})
	if err != nil {
		t.Fatal(err)
	}
	res, err = search.Execute(searchInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(fmt.Sprint(res.Content), "project") || !strings.Contains(fmt.Sprint(res.Content), "persistent") {
		t.Fatalf("unexpected search result: %+v", res)
	}
}

func TestMemoryRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SKAWLD_MEMORY_DIR", dir)
	tool := MemoryReadTool{}
	input, err := tool.Validate(map[string]interface{}{"name": "../secret"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(input, core.ToolContext{Context: context.Background(), CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "path separators") {
		t.Fatalf("expected traversal rejection, got %+v", res)
	}
}

func TestSessionSearchFindsStoredMessages(t *testing.T) {
	store := sessions.NewInMemoryStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "s2", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessages(ctx, "s1", []core.Message{{Role: "user", Content: []core.ContentBlock{core.Text("alpha target phrase")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessages(ctx, "s2", []core.Message{{Role: "assistant", Content: []core.ContentBlock{core.Text("unrelated")}}}); err != nil {
		t.Fatal(err)
	}

	tool := SessionSearchTool{}
	input, err := tool.Validate(map[string]interface{}{"query": "target", "limit": 10, "max_sessions": 10})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(input, core.ToolContext{Context: ctx, SessionStore: store})
	if err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprint(res.Content)
	if res.IsError || !strings.Contains(content, "session=s1") || !strings.Contains(content, "target phrase") {
		t.Fatalf("unexpected session search result: %+v", res)
	}
}
