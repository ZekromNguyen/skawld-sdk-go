package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type GlobTool struct{}

func (GlobTool) Name() string { return "Glob" }
func (GlobTool) Description() string {
	return "Finds files matching a glob-style pattern. Results are sorted by modification time and capped at 1000."
}
func (GlobTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"pattern": map[string]interface{}{"type": "string"}, "path": map[string]interface{}{"type": "string"}},
		"required":   []string{"pattern"},
	}
}
func (GlobTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (GlobTool) ParallelSafe() bool    { return true }
func (t GlobTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	pattern, ok := asString(raw["pattern"])
	if !ok || pattern == "" {
		return nil, core.NewToolExecutionError(t.Name(), "pattern must be a non-empty string")
	}
	out := map[string]interface{}{"pattern": filepath.ToSlash(pattern)}
	if p, ok := asString(raw["path"]); ok {
		out["path"] = p
	}
	return out, nil
}
func (t GlobTool) Summarize(input map[string]interface{}) string {
	if p, ok := asString(input["path"]); ok && p != "" {
		return fmt.Sprintf("Glob %s in %s", input["pattern"], p)
	}
	return fmt.Sprintf("Glob %s", input["pattern"])
}
func staticBase(pattern string) (base string, rest string) {
	metaIdx := strings.IndexAny(pattern, "*?[{")
	if metaIdx == -1 {
		return pattern, "."
	}
	dir := filepath.ToSlash(filepath.Dir(pattern[:metaIdx]))
	if dir == "." {
		return dir, pattern
	}
	// +1 for the separator
	if len(dir) < len(pattern) && pattern[len(dir)] == '/' {
		return dir, pattern[len(dir)+1:]
	}
	return dir, pattern[len(dir):]
}

func (t GlobTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	root := ctx.CWD
	if p, ok := asString(input["path"]); ok && p != "" {
		resolved, err := resolveFilesystem(ctx, p, core.FilesystemResolveSearch)
		if err != nil {
			return core.ToolResult{Content: "Glob error: " + err.Error(), Summary: "glob error", IsError: true}, nil
		}
		root = resolved
	} else if _, err := resolveFilesystem(ctx, ".", core.FilesystemResolveSearch); err != nil {
		return core.ToolResult{Content: "Glob error: " + err.Error(), Summary: "glob error", IsError: true}, nil
	}
	pattern := filepath.ToSlash(input["pattern"].(string))
	searchRoot := root

	if filepath.IsAbs(pattern) {
		base, rest := staticBase(pattern)
		resolved, err := resolveFilesystem(ctx, base, core.FilesystemResolveSearch)
		if err != nil {
			return core.ToolResult{Content: "Glob error: " + err.Error(), Summary: "glob error", IsError: true}, nil
		}
		searchRoot = resolved
		pattern = rest
	}

	if rg, ok := executable("rg"); ok {
		files, err := globWithRipgrep(ctx, rg, searchRoot, pattern)
		if err == nil {
			if searchRoot != root {
				for i, f := range files {
					files[i].path = filepath.ToSlash(filepath.Join(searchRoot, f.path))
					files[i].path, _ = filepath.Rel(root, files[i].path)
					files[i].path = filepath.ToSlash(files[i].path)
				}
			}
			return formatGlobResults(files, root)
		}
	}
	files, err := globFallback(ctx, searchRoot, pattern)
	if searchRoot != root {
		for i, f := range files {
			files[i].path = filepath.ToSlash(filepath.Join(searchRoot, f.path))
			files[i].path, _ = filepath.Rel(root, files[i].path)
			files[i].path = filepath.ToSlash(files[i].path)
		}
	}
	if err != nil {
		return core.ToolResult{Content: "Glob error: " + err.Error(), Summary: "glob error", IsError: true}, nil
	}
	return formatGlobResults(files, root)
}

type globHit struct {
	path  string
	mtime int64
}

func globWithRipgrep(ctx core.ToolContext, rg, root, pattern string) ([]globHit, error) {
	output, exitCode, err := runCommand(ctx.Context, root, rg, "--files", "--glob", pattern, "--glob", "!.*")
	if err != nil {
		return nil, err
	}
	if exitCode != 0 && strings.TrimSpace(output) == "" {
		return nil, nil
	}
	var hits []globHit
	for _, line := range strings.Split(output, "\n") {
		rel := strings.TrimSpace(line)
		if rel == "" {
			continue
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(filepath.Base(rel), ".") {
			continue
		}
		info, _ := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		var mt int64
		if info != nil {
			mt = info.ModTime().UnixNano()
		}
		hits = append(hits, globHit{path: rel, mtime: mt})
	}
	return hits, nil
}

func globFallback(ctx core.ToolContext, root, pattern string) ([]globHit, error) {
	type hit struct {
		path  string
		mtime int64
	}
	var hits []globHit
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctx.Context.Err() != nil {
			return ctx.Context.Err()
		}
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				if d.Name() == ".git" || d.Name() == ".hg" || d.Name() == ".svn" || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if globMatch(pattern, rel) {
			info, _ := d.Info()
			var mt int64
			if info != nil {
				mt = info.ModTime().UnixNano()
			}
			hits = append(hits, globHit{rel, mt})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

func formatGlobResults(hits []globHit, root string) (core.ToolResult, error) {
	if len(hits) == 0 {
		return core.ToolResult{Content: "No matches found.", Summary: "no matches"}, nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mtime > hits[j].mtime })
	limit := min(1000, len(hits))
	lines := make([]string, 0, limit)
	for _, h := range hits[:limit] {
		lines = append(lines, h.path)
	}
	out := strings.Join(lines, "\n")
	if len(hits) > limit {
		out += fmt.Sprintf("\n... (truncated to %d of %d results)", limit, len(hits))
	}
	return core.ToolResult{Content: out, Summary: fmt.Sprintf("%d file(s) matched", limit)}, nil
}

func matchGlob(pattern, rel string) bool {
	return globMatch(pattern, rel)
}
