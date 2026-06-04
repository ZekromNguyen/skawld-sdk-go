package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type GrepTool struct{}

func (GrepTool) Name() string { return "Grep" }
func (GrepTool) Description() string {
	return "Searches file contents by regex. Uses ripgrep when available; falls back to a pure-Go implementation. Output modes: files_with_matches (default), content, count. Supports -i, -n, -A/-B/-C context, multiline, glob filter, type filter."
}
func (GrepTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern":     map[string]interface{}{"type": "string", "description": "Regex pattern to search for."},
			"path":        map[string]interface{}{"type": "string", "description": "Root directory to search. Defaults to working directory."},
			"glob":        map[string]interface{}{"type": "string", "description": "File filter glob pattern, e.g. '**/*.ts'."},
			"type":        map[string]interface{}{"type": "string", "description": "File type alias (e.g. 'ts', 'js', 'py'). Passed as rg --type."},
			"output_mode": map[string]interface{}{"type": "string", "enum": []string{"files_with_matches", "content", "count"}},
			"-i":          map[string]interface{}{"type": "boolean"},
			"-n":          map[string]interface{}{"type": "boolean"},
			"-A":          map[string]interface{}{"type": "number"},
			"-B":          map[string]interface{}{"type": "number"},
			"-C":          map[string]interface{}{"type": "number"},
			"multiline":   map[string]interface{}{"type": "boolean"},
			"head_limit":  map[string]interface{}{"type": "number"},
		},
		"required": []string{"pattern"},
	}
}
func (GrepTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (GrepTool) ParallelSafe() bool    { return true }

func coerceNonNegativeInt(raw map[string]interface{}, key string) (int, bool) {
	if val, ok := raw[key]; ok {
		switch n := val.(type) {
		case float64:
			return int(n), true
		case int:
			return int(n), true
		case string:
			i, _ := strconv.Atoi(n)
			return i, true
		}
	}
	return -1, false
}

func (t GrepTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	pattern, ok := asString(raw["pattern"])
	if !ok || pattern == "" {
		return nil, core.NewToolExecutionError(t.Name(), "pattern is required and must be a non-empty string")
	}
	out := map[string]interface{}{
		"pattern":     pattern,
		"output_mode": "files_with_matches",
	}
	if mode, ok := asString(raw["output_mode"]); ok && mode != "" {
		switch mode {
		case "files_with_matches", "content", "count":
			out["output_mode"] = mode
		default:
			return nil, core.NewToolExecutionError(t.Name(), "output_mode must be one of: files_with_matches, content, count")
		}
	}
	if p, ok := asString(raw["path"]); ok {
		out["path"] = p
	}
	if g, ok := asString(raw["glob"]); ok {
		out["glob"] = filepath.ToSlash(g)
	}
	if typ, ok := asString(raw["type"]); ok {
		out["type"] = typ
	}
	out["-i"] = asBool(raw["-i"])
	out["-n"] = asBool(raw["-n"])
	out["multiline"] = asBool(raw["multiline"])

	if a, ok := coerceNonNegativeInt(raw, "-A"); ok && a >= 0 {
		out["-A"] = a
	}
	if b, ok := coerceNonNegativeInt(raw, "-B"); ok && b >= 0 {
		out["-B"] = b
	}
	if c, ok := coerceNonNegativeInt(raw, "-C"); ok && c >= 0 {
		out["-C"] = c
	}
	if hl, ok := coerceNonNegativeInt(raw, "head_limit"); ok && hl >= 0 {
		out["head_limit"] = hl
	} else {
		out["head_limit"] = 250 // default
	}

	return out, nil
}

func (t GrepTool) Summarize(input map[string]interface{}) string {
	mode := input["output_mode"].(string)
	p := ""
	if pathVal, ok := asString(input["path"]); ok && pathVal != "" {
		p = fmt.Sprintf(" in %s", pathVal)
	}
	return fmt.Sprintf("Grep %q (%s)%s", input["pattern"], mode, p)
}

func parseOutputAndTruncate(rawOutput string, headLimit int) (core.ToolResult, error) {
	if rawOutput == "" || strings.TrimSpace(rawOutput) == "" {
		return core.ToolResult{Content: "No matches found.", Summary: "no matches"}, nil
	}

	lines := strings.Split(strings.TrimSuffix(rawOutput, "\n"), "\n")
	total := len(lines)

	if total > headLimit {
		lines = lines[:headLimit]
	}
	out := strings.Join(lines, "\n")
	if total > headLimit {
		out += fmt.Sprintf("\n... (truncated to %d of %d lines)", headLimit, total)
	}

	return core.ToolResult{
		Content: truncate(out, 30000),
		Summary: fmt.Sprintf("Grep matched %d line(s)", min(total, headLimit)),
	}, nil
}

func (t GrepTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	rootRaw, _ := asString(input["path"])
	searchRoot := ctx.CWD
	if rootRaw != "" {
		searchRoot = resolvePath(rootRaw, ctx.CWD)
	}
	headLimit := input["head_limit"].(int)

	var rawOutput string

	if rg, ok := executable("rg"); ok {
		args := buildRgArgs(input, searchRoot)
		out, exitCode, err := runCommand(ctx.Context, searchRoot, rg, args...)
		if err != nil && exitCode != 1 { // exitCode 1 means no matches in ripgrep
			if ctx.Context.Err() != nil {
				return core.ToolResult{Content: "Grep search aborted.", Summary: "aborted", IsError: true}, nil
			}
			return core.ToolResult{Content: "Grep error: " + err.Error(), Summary: "grep error", IsError: true}, nil
		}
		rawOutput = out
		if exitCode == 1 && strings.TrimSpace(rawOutput) == "" {
			rawOutput = ""
		}
	} else {
		fbOut, err := runGrepFallback(ctx, input, searchRoot)
		if err != nil {
			if ctx.Context.Err() != nil {
				return core.ToolResult{Content: "Grep search aborted.", Summary: "aborted", IsError: true}, nil
			}
			return core.ToolResult{Content: "Grep error: " + err.Error(), Summary: "grep error", IsError: true}, nil
		}
		rawOutput = fbOut
	}

	return parseOutputAndTruncate(rawOutput, headLimit)
}

func buildRgArgs(input map[string]interface{}, searchRoot string) []string {
	args := []string{"--max-columns", "500"}
	mode := input["output_mode"].(string)

	if mode == "files_with_matches" {
		args = append(args, "--files-with-matches")
	} else if mode == "count" {
		args = append(args, "--count")
	}

	if asBool(input["-i"]) {
		args = append(args, "--ignore-case")
	}
	if asBool(input["-n"]) && mode == "content" {
		args = append(args, "--line-number")
	}
	if asBool(input["multiline"]) {
		args = append(args, "--multiline", "--multiline-dotall")
	}

	if ctxC, ok := input["-C"].(int); ok {
		args = append(args, "-C", strconv.Itoa(ctxC))
	} else {
		if ctxA, ok := input["-A"].(int); ok {
			args = append(args, "-A", strconv.Itoa(ctxA))
		}
		if ctxB, ok := input["-B"].(int); ok {
			args = append(args, "-B", strconv.Itoa(ctxB))
		}
	}

	if glob, ok := asString(input["glob"]); ok && glob != "" {
		args = append(args, "--glob", glob)
	}
	if typ, ok := asString(input["type"]); ok && typ != "" {
		args = append(args, "--type", typ)
	}

	args = append(args, input["pattern"].(string), searchRoot)
	return args
}

var typeGlobs = map[string][]string{
	"ts":    {"**/*.ts", "**/*.tsx"},
	"js":    {"**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs"},
	"py":    {"**/*.py"},
	"go":    {"**/*.go"},
	"rs":    {"**/*.rs"},
	"md":    {"**/*.md", "**/*.markdown"},
	"json":  {"**/*.json"},
	"yaml":  {"**/*.yaml", "**/*.yml"},
	"html":  {"**/*.html", "**/*.htm"},
	"css":   {"**/*.css"},
	"sh":    {"**/*.sh"},
	"c":     {"**/*.c", "**/*.h"},
	"cpp":   {"**/*.cpp", "**/*.cc", "**/*.cxx", "**/*.hpp", "**/*.hxx"},
	"java":  {"**/*.java"},
	"rb":    {"**/*.rb"},
	"php":   {"**/*.php"},
	"swift": {"**/*.swift"},
	"kt":    {"**/*.kt"},
}

// ---- FALLBACK ----

type matchLine struct {
	lineNo int
	text   string
}

type fileMatches struct {
	relPath string
	matches []matchLine
}

func runGrepFallback(ctx core.ToolContext, input map[string]interface{}, searchRoot string) (string, error) {
	mode := input["output_mode"].(string)
	pat := input["pattern"].(string)

	flags := ""
	if asBool(input["-i"]) {
		flags += "(?i)"
	}
	if asBool(input["multiline"]) {
		flags += "(?s)(?m)" // dot matches newline and ^$ match lines
	}
	re, err := regexp.Compile(flags + pat)
	if err != nil {
		return "Invalid regex: " + err.Error(), nil // Like TS
	}

	var typePatterns []string
	if typ, ok := asString(input["type"]); ok && typ != "" {
		if tg, ok := typeGlobs[typ]; ok {
			typePatterns = tg
		}
	}
	globStr, _ := asString(input["glob"])

	var results []fileMatches
	fileLines := make(map[string][]string)

	err = filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		if ctx.Context.Err() != nil {
			return ctx.Context.Err()
		}
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				if d.Name() == ".git" || d.Name() == ".hg" || d.Name() == ".svn" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		rel, _ := filepath.Rel(searchRoot, path)
		rel = filepath.ToSlash(rel)

		if globStr != "" {
			if !matchGlob(globStr, rel) {
				return nil
			}
		}
		if len(typePatterns) > 0 {
			matchedType := false
			for _, tp := range typePatterns {
				if matchGlob(tp, rel) {
					matchedType = true
					break
				}
			}
			if !matchedType {
				return nil
			}
		}

		b, err := os.ReadFile(path)
		if err != nil || hasNullByteBytes(b) {
			return nil
		}
		content := string(b)
		lines := strings.Split(content, "\n")
		var matches []matchLine
		for i, line := range lines {
			if re.MatchString(line) {
				matches = append(matches, matchLine{lineNo: i + 1, text: line})
			}
		}
		if len(matches) > 0 {
			results = append(results, fileMatches{relPath: rel, matches: matches})
			if mode == "content" {
				fileLines[rel] = lines
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return "", nil
	}

	switch mode {
	case "files_with_matches":
		return renderFilesWithMatches(results), nil
	case "count":
		return renderCount(results), nil
	case "content":
		return renderContent(results, input, fileLines), nil
	}
	return "", nil
}

func hasNullByteBytes(buf []byte) bool {
	limit := 8192
	if len(buf) < limit {
		limit = len(buf)
	}
	for i := 0; i < limit; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

func renderFilesWithMatches(results []fileMatches) string {
	var lines []string
	for _, r := range results {
		lines = append(lines, r.relPath)
	}
	return strings.Join(lines, "\n")
}

func renderCount(results []fileMatches) string {
	var lines []string
	for _, r := range results {
		lines = append(lines, fmt.Sprintf("%s:%d", r.relPath, len(r.matches)))
	}
	return strings.Join(lines, "\n")
}

type intRange struct {
	start int
	end   int
}

func renderContent(results []fileMatches, input map[string]interface{}, fileLines map[string][]string) string {
	ctxA := 0
	ctxB := 0
	if c, ok := input["-C"].(int); ok {
		ctxA = c
		ctxB = c
	} else {
		if a, ok := input["-A"].(int); ok {
			ctxA = a
		}
		if b, ok := input["-B"].(int); ok {
			ctxB = b
		}
	}
	showLineNo := asBool(input["-n"])
	var out []string

	for _, r := range results {
		fc := fileLines[r.relPath]
		var ranges []intRange
		for _, m := range r.matches {
			start := m.lineNo - 1 - ctxB
			if start < 0 {
				start = 0
			}
			end := m.lineNo - 1 + ctxA
			if end >= len(fc) {
				end = len(fc) - 1
			}

			if len(ranges) > 0 {
				lastIdx := len(ranges) - 1
				if start <= ranges[lastIdx].end+1 {
					if end > ranges[lastIdx].end {
						ranges[lastIdx].end = end
					}
				} else {
					ranges = append(ranges, intRange{start, end})
				}
			} else {
				ranges = append(ranges, intRange{start, end})
			}
		}

		prevEnd := -1
		for _, rg := range ranges {
			if prevEnd >= 0 && rg.start > prevEnd+1 {
				out = append(out, "--")
			}
			for i := rg.start; i <= rg.end; i++ {
				lineText := fc[i]
				isMatch := false
				for _, m := range r.matches {
					if m.lineNo == i+1 {
						isMatch = true
						break
					}
				}
				if showLineNo {
					sep := "-"
					if isMatch {
						sep = ":"
					}
					out = append(out, fmt.Sprintf("%s%s%d%s%s", r.relPath, sep, i+1, sep, lineText))
				} else {
					out = append(out, fmt.Sprintf("%s:%s", r.relPath, lineText))
				}
			}
			prevEnd = rg.end
		}
	}
	return strings.Join(out, "\n")
}
