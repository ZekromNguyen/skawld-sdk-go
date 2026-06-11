package tools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
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

func (t GrepTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseGrepInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
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
		resolved, err := resolveFilesystem(ctx, rootRaw, core.FilesystemResolveSearch)
		if err != nil {
			return core.ToolResult{Content: "Grep error: " + err.Error(), Summary: "grep error", IsError: true}, nil
		}
		searchRoot = resolved
	} else if _, err := resolveFilesystem(ctx, ".", core.FilesystemResolveSearch); err != nil {
		return core.ToolResult{Content: "Grep error: " + err.Error(), Summary: "grep error", IsError: true}, nil
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
	multiline := asBool(input["multiline"])

	flags := ""
	if asBool(input["-i"]) {
		flags += "(?i)"
	}
	if multiline {
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
	if !multiline {
		return runGrepFallbackStreaming(ctx, input, searchRoot, re, globStr, typePatterns)
	}

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

type grepOutputCollector struct {
	mode      string
	input     map[string]interface{}
	headLimit int
	lines     []string
	counts    []string
	bytes     int
}

const grepFallbackOutputByteLimit = 30000

func newGrepOutputCollector(input map[string]interface{}) *grepOutputCollector {
	headLimit := input["head_limit"].(int)
	if headLimit < 0 {
		headLimit = 0
	}
	return &grepOutputCollector{mode: input["output_mode"].(string), input: input, headLimit: headLimit}
}

func (c *grepOutputCollector) done() bool {
	return (c.headLimit > 0 && c.lineCount() >= c.headLimit) || c.bytes >= grepFallbackOutputByteLimit
}

func (c *grepOutputCollector) addLine(line string) {
	if c.done() {
		return
	}
	if c.bytes+len(line)+1 > grepFallbackOutputByteLimit {
		remaining := grepFallbackOutputByteLimit - c.bytes
		if remaining <= 1 {
			c.bytes = grepFallbackOutputByteLimit
			return
		}
		line = line[:remaining-1]
	}
	c.bytes += len(line) + 1
	c.lines = append(c.lines, line)
}

func (c *grepOutputCollector) addFile(rel string) {
	c.addLine(rel)
}

func (c *grepOutputCollector) addCount(rel string, count int) {
	if count > 0 {
		if c.done() {
			return
		}
		line := fmt.Sprintf("%s:%d", rel, count)
		if c.bytes+len(line)+1 > grepFallbackOutputByteLimit {
			c.bytes = grepFallbackOutputByteLimit
			return
		}
		c.bytes += len(line) + 1
		c.counts = append(c.counts, line)
	}
}

func (c *grepOutputCollector) addContent(rel string, lineNo int, text string, isMatch bool) {
	showLineNo := asBool(c.input["-n"])
	if showLineNo {
		sep := "-"
		if isMatch {
			sep = ":"
		}
		c.addLine(fmt.Sprintf("%s%s%d%s%s", rel, sep, lineNo, sep, text))
		return
	}
	c.addLine(fmt.Sprintf("%s:%s", rel, text))
}

func (c *grepOutputCollector) String() string {
	lines := c.lines
	if c.mode == "count" {
		lines = c.counts
	}
	return strings.Join(lines, "\n")
}

func (c *grepOutputCollector) lineCount() int {
	if c.mode == "count" {
		return len(c.counts)
	}
	return len(c.lines)
}

func runGrepFallbackStreaming(ctx core.ToolContext, input map[string]interface{}, searchRoot string, re *regexp.Regexp, globStr string, typePatterns []string) (string, error) {
	collector := newGrepOutputCollector(input)
	err := filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		if ctx.Context.Err() != nil {
			return ctx.Context.Err()
		}
		if collector.done() {
			return filepath.SkipAll
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
		if !grepPathAllowed(rel, globStr, typePatterns) {
			return nil
		}
		return scanGrepFile(ctx, path, rel, re, input, collector)
	})
	if err != nil {
		return "", err
	}
	return collector.String(), nil
}

func grepPathAllowed(rel, globStr string, typePatterns []string) bool {
	if globStr != "" && !matchGlob(globStr, rel) {
		return false
	}
	if len(typePatterns) == 0 {
		return true
	}
	for _, tp := range typePatterns {
		if matchGlob(tp, rel) {
			return true
		}
	}
	return false
}

func scanGrepFile(ctx core.ToolContext, path, rel string, re *regexp.Regexp, input map[string]interface{}, collector *grepOutputCollector) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if hasNullByteReader(f) {
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	switch collector.mode {
	case "files_with_matches":
		return scanGrepFileNameMode(ctx, f, rel, re, collector)
	case "count":
		return scanGrepCountMode(ctx, f, rel, re, collector)
	case "content":
		return scanGrepContentMode(ctx, f, rel, re, input, collector)
	default:
		return nil
	}
}

func scanGrepFileNameMode(ctx core.ToolContext, r io.Reader, rel string, re *regexp.Regexp, collector *grepOutputCollector) error {
	reader := bufio.NewReader(r)
	for {
		if err := ctx.Context.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if line != "" && re.MatchString(strings.TrimRight(line, "\r\n")) {
			collector.addFile(rel)
			return nil
		}
		if errorsIsEOF(err) {
			return nil
		}
		if err != nil {
			return nil
		}
	}
}

func scanGrepCountMode(ctx core.ToolContext, r io.Reader, rel string, re *regexp.Regexp, collector *grepOutputCollector) error {
	reader := bufio.NewReader(r)
	count := 0
	for {
		if err := ctx.Context.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if line != "" && re.MatchString(strings.TrimRight(line, "\r\n")) {
			count++
		}
		if errorsIsEOF(err) {
			collector.addCount(rel, count)
			return nil
		}
		if err != nil {
			return nil
		}
	}
}

func scanGrepContentMode(ctx core.ToolContext, r io.Reader, rel string, re *regexp.Regexp, input map[string]interface{}, collector *grepOutputCollector) error {
	ctxA, ctxB := grepContext(input)
	reader := bufio.NewReader(r)
	var before []matchLine
	var pendingAfter int
	emitted := false
	lastOutputLine := 0
	for lineNo := 1; ; lineNo++ {
		if err := ctx.Context.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if line == "" && errorsIsEOF(err) {
			return nil
		}
		text := strings.TrimRight(line, "\r\n")
		isMatch := re.MatchString(text)
		if isMatch {
			firstOutputLine := lineNo
			if len(before) > 0 {
				firstOutputLine = before[0].lineNo
			}
			if emitted && firstOutputLine > lastOutputLine+1 {
				collector.addLine("--")
			}
			for _, prev := range before {
				if prev.lineNo > lastOutputLine {
					collector.addContent(rel, prev.lineNo, prev.text, false)
					lastOutputLine = prev.lineNo
				}
			}
			before = nil
			if lineNo > lastOutputLine {
				collector.addContent(rel, lineNo, text, true)
				lastOutputLine = lineNo
			}
			pendingAfter = ctxA
			emitted = true
		} else if pendingAfter > 0 {
			if lineNo > lastOutputLine {
				collector.addContent(rel, lineNo, text, false)
				lastOutputLine = lineNo
			}
			pendingAfter--
		} else {
			if ctxB > 0 {
				before = append(before, matchLine{lineNo: lineNo, text: text})
				if len(before) > ctxB {
					copy(before, before[1:])
					before = before[:ctxB]
				}
			}
		}
		if collector.done() {
			return filepath.SkipAll
		}
		if errorsIsEOF(err) {
			return nil
		}
		if err != nil {
			return nil
		}
	}
}

func grepContext(input map[string]interface{}) (int, int) {
	ctxA := 0
	ctxB := 0
	if c, ok := input["-C"].(int); ok {
		return c, c
	}
	if a, ok := input["-A"].(int); ok {
		ctxA = a
	}
	if b, ok := input["-B"].(int); ok {
		ctxB = b
	}
	return ctxA, ctxB
}

func hasNullByteReader(r io.Reader) bool {
	buf := make([]byte, 8192)
	n, _ := io.ReadFull(r, buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func errorsIsEOF(err error) bool {
	return errors.Is(err, io.EOF)
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
