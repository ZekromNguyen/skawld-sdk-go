package frontmatter

import "strings"

// Document is a markdown document with optional YAML-like frontmatter.
type Document struct {
	Metadata Metadata
	Body     string
}

// Metadata contains the small frontmatter subset supported by Skawld local
// skill and subagent files.
type Metadata map[string]interface{}

// ParseDocument splits a markdown document into frontmatter metadata and body.
// It intentionally supports only the scalar and string-slice forms used by
// SKILL.md and subagent definition files.
func ParseDocument(input string) Document {
	body := input
	meta := Metadata{}
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		normalized := strings.ReplaceAll(body, "\r\n", "\n")
		rest := strings.TrimPrefix(normalized, "---\n")
		if end := strings.Index(rest, "\n---\n"); end >= 0 {
			meta = parse(rest[:end])
			body = rest[end+len("\n---\n"):]
		}
	}
	return Document{Metadata: meta, Body: body}
}

// String returns a scalar metadata value.
func (m Metadata) String(key string) string {
	s, _ := m[key].(string)
	return s
}

// Strings returns a normalized string slice metadata value.
func (m Metadata) Strings(key string) []string {
	switch vals := m[key].(type) {
	case []string:
		out := make([]string, 0, len(vals))
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				out = append(out, strings.TrimSpace(v))
			}
		}
		return out
	default:
		return nil
	}
}

func parse(input string) Metadata {
	out := Metadata{}
	lines := strings.Split(input, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "" {
			var items []string
			for i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(next, "- ") {
					break
				}
				items = append(items, trimScalar(strings.TrimSpace(strings.TrimPrefix(next, "- "))))
				i++
			}
			out[key] = items
			continue
		}
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(val, "["), "]"))
			if inner == "" {
				out[key] = []string{}
				continue
			}
			var items []string
			for _, part := range strings.Split(inner, ",") {
				items = append(items, trimScalar(strings.TrimSpace(part)))
			}
			out[key] = items
			continue
		}
		out[key] = trimScalar(val)
	}
	return out
}

func trimScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}
