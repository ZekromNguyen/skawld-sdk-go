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

func TestEditRequiresReadFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := NewFileReadTracker()
	ctx := core.ToolContext{
		Context:         context.Background(),
		CWD:             dir,
		FileReadTracker: tracker,
		SessionID:       "s",
		RunID:           "r",
		SessionStore:    sessions.NewInMemoryStore(),
	}
	edit := EditTool{}
	input, err := edit.Validate(map[string]interface{}{"file_path": "file.txt", "old_string": "alpha", "new_string": "beta"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := edit.Execute(input, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected edit before read to fail")
	}
	read := ReadTool{}
	readInput, _ := read.Validate(map[string]interface{}{"file_path": "file.txt"})
	if _, err := read.Execute(readInput, ctx); err != nil {
		t.Fatal(err)
	}
	res, err = edit.Execute(input, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected edit after read to succeed: %v", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "beta\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestReadSupportsOffsetsAndTruncatesLongLines(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 2105)
	path := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(path, []byte("one\n"+long+"\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.ToolContext{
		Context:         context.Background(),
		CWD:             dir,
		FileReadTracker: NewFileReadTracker(),
		SessionID:       "s",
		RunID:           "r",
		SessionStore:    sessions.NewInMemoryStore(),
	}
	read := ReadTool{}
	input, err := read.Validate(map[string]interface{}{"file_path": "large.txt", "offset": 2, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	res, err := read.Execute(input, ctx)
	if err != nil {
		t.Fatal(err)
	}
	content := res.Content.(string)
	if !strings.HasPrefix(content, "2\t") {
		t.Fatalf("expected line 2 prefix, got %q", content[:min(len(content), 20)])
	}
	if !strings.Contains(content, "chars truncated") {
		t.Fatalf("expected truncation note, got %q", content)
	}
}

func TestEditPreservesCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := NewFileReadTracker()
	ctx := core.ToolContext{
		Context:         context.Background(),
		CWD:             dir,
		FileReadTracker: tracker,
		SessionID:       "s",
		RunID:           "r",
		SessionStore:    sessions.NewInMemoryStore(),
	}
	read := ReadTool{}
	readInput, _ := read.Validate(map[string]interface{}{"file_path": "crlf.txt"})
	if _, err := read.Execute(readInput, ctx); err != nil {
		t.Fatal(err)
	}
	edit := EditTool{}
	editInput, err := edit.Validate(map[string]interface{}{"file_path": "crlf.txt", "old_string": "alpha\r\nbeta", "new_string": "one\ntwo"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := edit.Execute(editInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("edit failed: %v", res.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\r\ntwo\r\n" {
		t.Fatalf("expected CRLF output, got %q", string(got))
	}
}
