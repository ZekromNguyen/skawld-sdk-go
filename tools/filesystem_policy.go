package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// FilesystemPolicy constrains built-in filesystem tools to approved roots.
// With no Roots, relative and absolute path handling is compatible with the
// historical unrestricted behavior. When Roots is set, absolute paths are
// allowed only when they resolve inside one of the configured roots. When
// FollowSymlinks is true, roots and requested paths are checked after symlink
// evaluation so symlink escapes are rejected; when false, the link path itself
// is checked.
type FilesystemPolicy struct {
	Roots          []string
	FollowSymlinks bool
}

func (p FilesystemPolicy) Resolve(cwd, raw string, mode core.FilesystemResolveMode) (string, error) {
	abs := resolvePath(raw, cwd)
	if len(p.Roots) == 0 {
		return abs, nil
	}
	checked := abs
	if p.FollowSymlinks {
		resolved, err := evalPathForPolicy(abs, mode)
		if err != nil {
			return "", err
		}
		checked = resolved
	}
	roots, err := p.normalizedRoots(cwd)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return abs, nil
	}
	if withinAnyRoot(checked, roots) {
		return abs, nil
	}
	return "", core.NewPermissionError(fmt.Sprintf("path %q is outside allowed filesystem roots", raw))
}

func (p FilesystemPolicy) normalizedRoots(cwd string) ([]string, error) {
	roots := make([]string, 0, len(p.Roots))
	for _, root := range p.Roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs := resolvePath(root, cwd)
		if p.FollowSymlinks {
			resolved, err := filepath.EvalSymlinks(abs)
			if err != nil {
				return nil, fmt.Errorf("resolve filesystem root %q: %w", root, err)
			}
			abs = resolved
		}
		roots = append(roots, filepath.Clean(abs))
	}
	return roots, nil
}

func resolveFilesystem(ctx core.ToolContext, raw string, mode core.FilesystemResolveMode) (string, error) {
	if ctx.Filesystem != nil {
		return ctx.Filesystem.Resolve(ctx.CWD, raw, mode)
	}
	return resolvePath(raw, ctx.CWD), nil
}

func evalPathForPolicy(abs string, mode core.FilesystemResolveMode) (string, error) {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) || mode == core.FilesystemResolveRead || mode == core.FilesystemResolveSearch {
		return "", err
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(abs), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
}

func withinAnyRoot(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		if samePath(path, root) {
			return true
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}
