package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
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

func TestEditMissingOldStringReturnsCandidateHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta target\ngamma\n"), 0o644); err != nil {
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
	readInput, _ := read.Validate(map[string]interface{}{"file_path": "file.txt"})
	if _, err := read.Execute(readInput, ctx); err != nil {
		t.Fatal(err)
	}
	edit := EditTool{}
	editInput, err := edit.Validate(map[string]interface{}{"file_path": "file.txt", "old_string": "beta missing", "new_string": "delta"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := edit.Execute(editInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "Nearby candidate") {
		t.Fatalf("expected nearby candidate hint, got %+v", res)
	}
}

func TestFilesystemPolicyRestrictsRoots(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	denied := filepath.Join(dir, "denied")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(allowed, "ok.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(denied, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := core.ToolContext{
		Context:         context.Background(),
		CWD:             allowed,
		Filesystem:      FilesystemPolicy{Roots: []string{allowed}},
		FileReadTracker: NewFileReadTracker(),
		SessionID:       "s",
		RunID:           "r",
		SessionStore:    sessions.NewInMemoryStore(),
	}
	read := ReadTool{}
	readInput, err := read.Validate(map[string]interface{}{"file_path": filepath.Join(allowed, "ok.txt")})
	if err != nil {
		t.Fatal(err)
	}
	res, err := read.Execute(readInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected allowed absolute read to succeed: %v", res.Content)
	}
	deniedRead, err := read.Validate(map[string]interface{}{"file_path": filepath.Join(denied, "secret.txt")})
	if err != nil {
		t.Fatal(err)
	}
	res, err = read.Execute(deniedRead, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "outside allowed filesystem roots") {
		t.Fatalf("expected denied read outside root, got %+v", res)
	}
	write := WriteTool{}
	writeInput, err := write.Validate(map[string]interface{}{"file_path": filepath.Join(denied, "new.txt"), "content": "no"})
	if err != nil {
		t.Fatal(err)
	}
	res, err = write.Execute(writeInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected write outside root to fail")
	}
	grep := GrepTool{}
	grepInput, err := grep.Validate(map[string]interface{}{"pattern": "secret", "path": denied})
	if err != nil {
		t.Fatal(err)
	}
	res, err = grep.Execute(grepInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected grep outside root to fail")
	}
	glob := GlobTool{}
	globInput, err := glob.Validate(map[string]interface{}{"pattern": "*.txt", "path": denied})
	if err != nil {
		t.Fatal(err)
	}
	res, err = glob.Execute(globInput, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected glob outside root to fail")
	}
}

func TestFilesystemPolicyRejectsSymlinkEscapeWhenFollowingSymlinks(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	denied := filepath.Join(dir, "denied")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(denied, "secret.txt")
	if err := os.WriteFile(target, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	ctx := core.ToolContext{
		Context:         context.Background(),
		CWD:             allowed,
		Filesystem:      FilesystemPolicy{Roots: []string{allowed}, FollowSymlinks: true},
		FileReadTracker: NewFileReadTracker(),
		SessionID:       "s",
		RunID:           "r",
		SessionStore:    sessions.NewInMemoryStore(),
	}
	read := ReadTool{}
	input, err := read.Validate(map[string]interface{}{"file_path": "link.txt"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := read.Execute(input, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "outside allowed filesystem roots") {
		t.Fatalf("expected symlink escape to be rejected, got %+v", res)
	}
}
