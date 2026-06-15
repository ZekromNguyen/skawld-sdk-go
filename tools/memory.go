package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/internal/frontmatter"
)

type MemoryReadTool struct{}
type MemoryWriteTool struct{}
type MemorySearchTool struct{}
type SessionSearchTool struct{}

func (MemoryReadTool) Name() string { return "MemoryRead" }
func (MemoryReadTool) Description() string {
	return "Read a named markdown memory from the configured memory directory."
}
func (MemoryReadTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "description": "Memory name or .md filename."},
		},
		"required": []string{"name"},
	}
}
func (MemoryReadTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (MemoryReadTool) ParallelSafe() bool    { return true }
func (t MemoryReadTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseMemoryReadInput(raw, t.Name())
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t MemoryReadTool) Summarize(input map[string]interface{}) string {
	return "Read memory " + input["name"].(string)
}
func (t MemoryReadTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	path, err := memoryPath(ctx, input["name"].(string), core.FilesystemResolveRead)
	if err != nil {
		return memoryError(t, input, err), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return memoryError(t, input, err), nil
	}
	doc := frontmatter.ParseDocument(string(data))
	content := doc.Body
	if strings.TrimSpace(content) == "" {
		content = "<memory is empty>"
	}
	return core.ToolResult{Content: content, Summary: t.Summarize(input)}, nil
}

func (MemoryWriteTool) Name() string { return "MemoryWrite" }
func (MemoryWriteTool) Description() string {
	return "Write or replace a named markdown memory with YAML frontmatter."
}
func (MemoryWriteTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":        map[string]interface{}{"type": "string", "description": "Memory name or .md filename."},
			"description": map[string]interface{}{"type": "string"},
			"content":     map[string]interface{}{"type": "string", "description": "Markdown memory body."},
			"metadata":    map[string]interface{}{"type": "object", "description": "Additional scalar or string-array metadata."},
		},
		"required": []string{"name", "content"},
	}
}
func (MemoryWriteTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (MemoryWriteTool) ParallelSafe() bool    { return false }
func (t MemoryWriteTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseMemoryWriteInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t MemoryWriteTool) Summarize(input map[string]interface{}) string {
	return "Write memory " + input["name"].(string)
}
func (t MemoryWriteTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	path, err := memoryPath(ctx, input["name"].(string), core.FilesystemResolveWrite)
	if err != nil {
		return memoryError(t, input, err), nil
	}
	in := memoryWriteInputFrom(input)
	if err := atomicWrite(path, []byte(formatMemoryDocument(in))); err != nil {
		return memoryError(t, input, err), nil
	}
	return core.ToolResult{Content: fmt.Sprintf("Memory %q written.", memoryDisplayName(path)), Summary: t.Summarize(input)}, nil
}

func (MemorySearchTool) Name() string { return "MemorySearch" }
func (MemorySearchTool) Description() string {
	return "Search markdown memories by name, description, metadata, and body."
}
func (MemorySearchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string"},
			"limit": map[string]interface{}{"type": "number", "description": "Maximum results, default 10, max 50."},
		},
		"required": []string{"query"},
	}
}
func (MemorySearchTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (MemorySearchTool) ParallelSafe() bool    { return true }
func (t MemorySearchTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseMemorySearchInput(raw, t.Name())
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t MemorySearchTool) Summarize(input map[string]interface{}) string {
	return "Search memories for " + input["query"].(string)
}
func (t MemorySearchTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := memorySearchInputFrom(input)
	root, err := memoryDir(ctx, core.FilesystemResolveRead)
	if err != nil {
		return memoryError(t, input, err), nil
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return core.ToolResult{Content: "No memories found.", Summary: "0 memory match(es)"}, nil
		}
		return memoryError(t, input, err), nil
	}
	if !info.IsDir() {
		return memoryError(t, input, fmt.Errorf("memory path is not a directory: %s", root)), nil
	}
	matches, err := searchMemories(ctx, root, in)
	if err != nil {
		return memoryError(t, input, err), nil
	}
	if len(matches) == 0 {
		return core.ToolResult{Content: "No memory matches found.", Summary: "0 memory match(es)"}, nil
	}
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(fmt.Sprintf("%d. %s", i+1, match.Name))
		if match.Description != "" {
			b.WriteString(" - ")
			b.WriteString(match.Description)
		}
		if match.Snippet != "" {
			b.WriteString("\n")
			b.WriteString(match.Snippet)
		}
	}
	return core.ToolResult{Content: b.String(), Summary: fmt.Sprintf("%d memory match(es)", len(matches))}, nil
}

func (SessionSearchTool) Name() string { return "SessionSearch" }
func (SessionSearchTool) Description() string {
	return "Search persisted session messages for text across recent sessions."
}
func (SessionSearchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query":        map[string]interface{}{"type": "string"},
			"limit":        map[string]interface{}{"type": "number", "description": "Maximum message matches, default 20, max 100."},
			"max_sessions": map[string]interface{}{"type": "number", "description": "Maximum recent sessions to scan, default 100, max 1000."},
		},
		"required": []string{"query"},
	}
}
func (SessionSearchTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (SessionSearchTool) ParallelSafe() bool    { return true }
func (t SessionSearchTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseSessionSearchInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t SessionSearchTool) Summarize(input map[string]interface{}) string {
	return "Search sessions for " + input["query"].(string)
}
func (t SessionSearchTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	if ctx.SessionStore == nil {
		return core.ToolResult{Content: "Session store is not configured.", Summary: t.Summarize(input), IsError: true}, nil
	}
	in := sessionSearchInputFrom(input)
	sessions, err := ctx.SessionStore.List(ctx.Context, in.MaxSessions, 0)
	if err != nil {
		return core.ToolResult{Content: "SessionSearch error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	matches, err := searchSessions(ctx, sessions, in)
	if err != nil {
		return core.ToolResult{Content: "SessionSearch error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	if len(matches) == 0 {
		return core.ToolResult{Content: "No session matches found.", Summary: "0 session match(es)"}, nil
	}
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(fmt.Sprintf("%d. session=%s seq=%d role=%s updated=%s", i+1, match.SessionID, match.Seq, match.Role, match.UpdatedAt))
		if match.Snippet != "" {
			b.WriteString("\n")
			b.WriteString(match.Snippet)
		}
	}
	return core.ToolResult{Content: b.String(), Summary: fmt.Sprintf("%d session match(es)", len(matches))}, nil
}

type memorySearchMatch struct {
	Name        string
	Description string
	Snippet     string
	Score       int
}

type sessionSearchMatch struct {
	SessionID string
	UpdatedAt string
	Seq       int
	Role      string
	Snippet   string
	Score     int
}

func memoryError(tool interface {
	Summarize(map[string]interface{}) string
}, input map[string]interface{}, err error) core.ToolResult {
	return core.ToolResult{Content: "Memory error: " + err.Error(), Summary: tool.Summarize(input), IsError: true}
}

func memoryDir(ctx core.ToolContext, mode core.FilesystemResolveMode) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("SKAWLD_MEMORY_DIR")); configured != "" {
		return resolveFilesystemPath(ctx, configured, mode)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		path := filepath.Join(home, ".claude", "memory")
		if mode == core.FilesystemResolveWrite {
			return resolveFilesystemPath(ctx, path, mode)
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return resolveFilesystemPath(ctx, path, mode)
		}
	}
	cwd := ctx.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return resolveFilesystemPath(ctx, filepath.Join(cwd, ".claude", "memory"), mode)
}

func resolveFilesystemPath(ctx core.ToolContext, path string, mode core.FilesystemResolveMode) (string, error) {
	if ctx.Filesystem != nil {
		return ctx.Filesystem.Resolve(ctx.CWD, path, mode)
	}
	return resolvePath(path, ctx.CWD), nil
}

func memoryPath(ctx core.ToolContext, name string, mode core.FilesystemResolveMode) (string, error) {
	root, err := memoryDir(ctx, mode)
	if err != nil {
		return "", err
	}
	fileName, err := sanitizeMemoryName(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, fileName)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", core.NewPermissionError("memory path escapes memory directory")
	}
	return path, nil
}

func sanitizeMemoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("memory name is empty")
	}
	name = filepath.ToSlash(name)
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("memory name must not contain path separators")
	}
	name = strings.TrimSuffix(name, ".md")
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid memory name")
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("memory name must not contain '..'")
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		default:
			return "", fmt.Errorf("memory name contains unsupported character %q", r)
		}
	}
	return b.String() + ".md", nil
}

func formatMemoryDocument(in memoryWriteInput) string {
	meta := map[string]interface{}{}
	for key, value := range in.Metadata {
		meta[key] = value
	}
	meta["name"] = in.Name
	if in.Description != "" {
		meta["description"] = in.Description
	}
	var keys []string
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("---\n")
	for _, key := range keys {
		if key == "" {
			continue
		}
		writeFrontmatterValue(&b, key, meta[key])
	}
	b.WriteString("---\n")
	b.WriteString(strings.TrimLeft(in.Content, "\r\n"))
	if !strings.HasSuffix(in.Content, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func writeFrontmatterValue(b *strings.Builder, key string, value interface{}) {
	switch v := value.(type) {
	case string:
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(quoteFrontmatterScalar(v))
		b.WriteByte('\n')
	case []string:
		b.WriteString(key)
		b.WriteString(":\n")
		for _, item := range v {
			b.WriteString("  - ")
			b.WriteString(quoteFrontmatterScalar(item))
			b.WriteByte('\n')
		}
	case []interface{}:
		b.WriteString(key)
		b.WriteString(":\n")
		for _, item := range v {
			if s, ok := item.(string); ok {
				b.WriteString("  - ")
				b.WriteString(quoteFrontmatterScalar(s))
				b.WriteByte('\n')
			}
		}
	case nil:
		return
	default:
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(quoteFrontmatterScalar(fmt.Sprint(v)))
		b.WriteByte('\n')
	}
}

func quoteFrontmatterScalar(value string) string {
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	return `"` + escaped + `"`
}

func searchMemories(ctx core.ToolContext, root string, in memorySearchInput) ([]memorySearchMatch, error) {
	query := strings.ToLower(in.Query)
	var matches []memorySearchMatch
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Context != nil {
			if err := ctx.Context.Err(); err != nil {
				return err
			}
		}
		if entry.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc := frontmatter.ParseDocument(string(data))
		name := doc.Metadata.String("name")
		if name == "" {
			name = memoryDisplayName(path)
		}
		description := doc.Metadata.String("description")
		haystack := strings.ToLower(name + "\n" + description + "\n" + metadataText(doc.Metadata) + "\n" + doc.Body)
		score := strings.Count(haystack, query)
		if score == 0 {
			return nil
		}
		if strings.Contains(strings.ToLower(name), query) {
			score += 5
		}
		if strings.Contains(strings.ToLower(description), query) {
			score += 3
		}
		matches = append(matches, memorySearchMatch{Name: name, Description: description, Snippet: snippet(doc.Body, in.Query), Score: score})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Name < matches[j].Name
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > in.Limit {
		matches = matches[:in.Limit]
	}
	return matches, nil
}

func searchSessions(ctx core.ToolContext, records []core.SessionRecord, in sessionSearchInput) ([]sessionSearchMatch, error) {
	query := strings.ToLower(in.Query)
	matches := make([]sessionSearchMatch, 0, in.Limit)
	for _, rec := range records {
		if ctx.Context != nil {
			if err := ctx.Context.Err(); err != nil {
				return nil, err
			}
		}
		messages, err := ctx.SessionStore.LoadMessages(ctx.Context, rec.ID)
		if err != nil {
			return nil, err
		}
		for _, msg := range messages {
			text := messageSearchText(msg.Message)
			score := strings.Count(strings.ToLower(text), query)
			if score == 0 {
				continue
			}
			matches = append(matches, sessionSearchMatch{
				SessionID: rec.ID,
				UpdatedAt: rec.UpdatedAt,
				Seq:       msg.Seq,
				Role:      msg.Message.Role,
				Snippet:   snippet(text, in.Query),
				Score:     score,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			if matches[i].UpdatedAt == matches[j].UpdatedAt {
				return matches[i].Seq < matches[j].Seq
			}
			return matches[i].UpdatedAt > matches[j].UpdatedAt
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > in.Limit {
		matches = matches[:in.Limit]
	}
	return matches, nil
}

func messageSearchText(msg core.Message) string {
	var parts []string
	for _, block := range msg.Content {
		switch block.Type {
		case core.BlockText:
			parts = append(parts, block.Text)
		case core.BlockToolUse:
			raw, _ := json.Marshal(block.Input)
			parts = append(parts, "tool_use "+block.Name+" "+string(raw))
		case core.BlockToolResult:
			parts = append(parts, "tool_result "+block.StringContent())
		case core.BlockThinking:
			parts = append(parts, block.Thinking)
		case core.BlockImage:
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func metadataText(meta frontmatter.Metadata) string {
	if len(meta) == 0 {
		return ""
	}
	var parts []string
	for key, value := range meta {
		parts = append(parts, key, fmt.Sprint(value))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func snippet(text, query string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)
	index := strings.Index(lowerText, lowerQuery)
	if index < 0 {
		return truncate(text, 240)
	}
	start := index - 90
	if start < 0 {
		start = 0
	}
	end := index + len(query) + 120
	if end > len(text) {
		end = len(text)
	}
	out := strings.TrimSpace(text[start:end])
	if start > 0 {
		out = "... " + out
	}
	if end < len(text) {
		out += " ..."
	}
	return truncate(out, 300)
}

func memoryDisplayName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
