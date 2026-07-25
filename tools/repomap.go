package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type RepoMapTool struct{}

func (RepoMapTool) Name() string { return "RepoMap" }
func (RepoMapTool) Description() string {
	return "Summarize the repository layout, Go packages, tests, and likely verification commands. Use early when orienting in an unfamiliar codebase."
}
func (RepoMapTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":        map[string]interface{}{"type": "string", "description": "Repository root to inspect. Defaults to working directory."},
			"max_entries": map[string]interface{}{"type": "number", "description": "Maximum package/file entries, default 80, max 300."},
		},
	}
}
func (RepoMapTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (RepoMapTool) ParallelSafe() bool    { return true }
func (t RepoMapTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	path, _ := asString(raw["path"])
	limit := asInt(raw["max_entries"], 80)
	if limit <= 0 {
		limit = 80
	}
	if limit > 300 {
		limit = 300
	}
	out := map[string]interface{}{"max_entries": limit}
	if strings.TrimSpace(path) != "" {
		out["path"] = strings.TrimSpace(path)
	}
	return out, nil
}
func (RepoMapTool) Summarize(input map[string]interface{}) string {
	if path, ok := asString(input["path"]); ok && path != "" {
		return "Map repository at " + path
	}
	return "Map repository"
}
func (t RepoMapTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	root := ctx.CWD
	if path, ok := asString(input["path"]); ok && path != "" {
		resolved, err := resolveFilesystem(ctx, path, core.FilesystemResolveSearch)
		if err != nil {
			return core.ToolResult{Content: "RepoMap error: " + err.Error(), Summary: "repo map error", IsError: true}, nil
		}
		root = resolved
	} else if _, err := resolveFilesystem(ctx, ".", core.FilesystemResolveSearch); err != nil {
		return core.ToolResult{Content: "RepoMap error: " + err.Error(), Summary: "repo map error", IsError: true}, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return core.ToolResult{Content: "RepoMap error: " + err.Error(), Summary: "repo map error", IsError: true}, nil
	}
	if !info.IsDir() {
		return core.ToolResult{Content: "RepoMap error: path is not a directory", Summary: "repo map error", IsError: true}, nil
	}
	report, err := buildRepoMap(ctx.Context, root, asInt(input["max_entries"], 80))
	if err != nil {
		return core.ToolResult{Content: "RepoMap error: " + err.Error(), Summary: "repo map error", IsError: true}, nil
	}
	return core.ToolResult{Content: report.format(), Summary: report.summary()}, nil
}

type repoMapReport struct {
	Root        string
	Module      string
	Directories []string
	Packages    []repoPackage
	Configs     []string
	Commands    []string
	Truncated   bool
}

type repoPackage struct {
	ImportPath string
	Dir        string
	Name       string
	Files      int
	Tests      int
}

func buildRepoMap(ctx context.Context, root string, limit int) (repoMapReport, error) {
	report := repoMapReport{Root: root}
	if module, err := moduleName(root); err == nil {
		report.Module = module
	}
	report.Configs = importantRepoFiles(root)
	if packages, err := goListPackages(ctx, root); err == nil && len(packages) > 0 {
		report.Packages = packages
	} else {
		packages, dirs, walkErr := walkRepoShape(ctx, root)
		if walkErr != nil {
			return report, walkErr
		}
		report.Packages = packages
		report.Directories = dirs
	}
	sort.Slice(report.Packages, func(i, j int) bool { return report.Packages[i].Dir < report.Packages[j].Dir })
	if len(report.Packages) > limit {
		report.Packages = report.Packages[:limit]
		report.Truncated = true
	}
	report.Commands = suggestedRepoCommands(root, len(report.Packages) > 0)
	return report, nil
}

func moduleName(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", nil
}

func goListPackages(ctx context.Context, root string) ([]repoPackage, error) {
	if _, ok := executable("go"); !ok {
		return nil, fmt.Errorf("go executable not found")
	}
	output, exitCode, err := runCommand(ctx, root, "go", "list", "-json", "./...")
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("go list exited %d", exitCode)
	}
	dec := json.NewDecoder(strings.NewReader(output))
	var packages []repoPackage
	for {
		var raw struct {
			ImportPath  string
			Dir         string
			Name        string
			GoFiles     []string
			TestGoFiles []string
		}
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		rel, _ := filepath.Rel(root, raw.Dir)
		if rel == "." {
			rel = "."
		}
		packages = append(packages, repoPackage{
			ImportPath: raw.ImportPath,
			Dir:        filepath.ToSlash(rel),
			Name:       raw.Name,
			Files:      len(raw.GoFiles),
			Tests:      len(raw.TestGoFiles),
		})
	}
	return packages, nil
}

func walkRepoShape(ctx context.Context, root string) ([]repoPackage, []string, error) {
	pkgByDir := map[string]*repoPackage{}
	dirSet := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipRepoMapDir(d.Name()) && path != root {
				return filepath.SkipDir
			}
			if path != root {
				rel, _ := filepath.Rel(root, path)
				dirSet[filepath.ToSlash(rel)] = struct{}{}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		dir := filepath.Dir(path)
		rel, _ := filepath.Rel(root, dir)
		if rel == "." {
			rel = "."
		}
		rel = filepath.ToSlash(rel)
		pkg := pkgByDir[rel]
		if pkg == nil {
			pkg = &repoPackage{Dir: rel}
			pkgByDir[rel] = pkg
		}
		if strings.HasSuffix(path, "_test.go") {
			pkg.Tests++
		} else {
			pkg.Files++
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	var packages []repoPackage
	for _, pkg := range pkgByDir {
		packages = append(packages, *pkg)
	}
	var dirs []string
	for dir := range dirSet {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	if len(dirs) > 40 {
		dirs = dirs[:40]
	}
	return packages, dirs, nil
}

func shouldSkipRepoMapDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".gocache", ".gomodcache", "node_modules", "vendor", "dist", "build", "target", "bin", "obj":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func importantRepoFiles(root string) []string {
	names := []string{"go.mod", "go.sum", "Makefile", "README.md", "TODO.md", "skawld.json", ".skawld/config.json", ".github/workflows/ci.yml"}
	var found []string
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
			found = append(found, name)
		}
	}
	return found
}

func suggestedRepoCommands(root string, hasGoPackages bool) []string {
	var commands []string
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err == nil {
		commands = append(commands, "make test")
	}
	if hasGoPackages {
		commands = append(commands, "go test ./...", "go vet ./...")
	}
	return commands
}

func (r repoMapReport) format() string {
	var b strings.Builder
	b.WriteString("Repository map\n")
	if r.Module != "" {
		b.WriteString("module: " + r.Module + "\n")
	}
	if len(r.Configs) > 0 {
		b.WriteString("important files: " + strings.Join(r.Configs, ", ") + "\n")
	}
	if len(r.Packages) > 0 {
		b.WriteString("\nGo packages:\n")
		for _, pkg := range r.Packages {
			label := pkg.Dir
			if pkg.ImportPath != "" {
				label = pkg.ImportPath
			}
			b.WriteString(fmt.Sprintf("- %s (%s): %d file(s), %d test file(s)\n", label, pkg.Dir, pkg.Files, pkg.Tests))
		}
		if r.Truncated {
			b.WriteString("- ... package list truncated\n")
		}
	} else if len(r.Directories) > 0 {
		b.WriteString("\nTop directories:\n")
		for _, dir := range r.Directories {
			b.WriteString("- " + dir + "\n")
		}
	}
	if len(r.Commands) > 0 {
		b.WriteString("\nLikely verification:\n")
		for _, command := range r.Commands {
			b.WriteString("- " + command + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r repoMapReport) summary() string {
	if len(r.Packages) > 0 {
		return fmt.Sprintf("repo map: %d package(s)", len(r.Packages))
	}
	return "repo map"
}
