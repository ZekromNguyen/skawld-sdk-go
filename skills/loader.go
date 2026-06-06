package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type Definition struct {
	Name         string
	Description  string
	WhenToUse    string
	ArgumentHint string
	AllowedTools []string
	Model        core.ModelID
	Body         string
	Path         string
}

type Manager struct {
	dir    string
	loaded bool
	order  []string
	items  map[string]Definition
}

func NewManager(dir string) *Manager {
	return &Manager{dir: dir, items: map[string]Definition{}}
}

func (m *Manager) Load() error {
	if m.loaded {
		return nil
	}
	m.loaded = true
	if strings.TrimSpace(m.dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(m.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(m.dir, entry.Name(), "SKILL.md")
		def, err := LoadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if def.Name == "" {
			def.Name = entry.Name()
		}
		if _, exists := m.items[def.Name]; exists {
			return fmt.Errorf("duplicate skill %q", def.Name)
		}
		m.items[def.Name] = def
		m.order = append(m.order, def.Name)
	}
	sort.Strings(m.order)
	return nil
}

func LoadFile(path string) (Definition, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, err
	}
	body := string(raw)
	meta := map[string]interface{}{}
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		normalized := strings.ReplaceAll(body, "\r\n", "\n")
		rest := strings.TrimPrefix(normalized, "---\n")
		if end := strings.Index(rest, "\n---\n"); end >= 0 {
			meta = parseFrontmatter(rest[:end])
			body = rest[end+len("\n---\n"):]
		}
	}
	def := Definition{
		Name:         stringMeta(meta, "name"),
		Description:  stringMeta(meta, "description"),
		WhenToUse:    stringMeta(meta, "when_to_use"),
		ArgumentHint: stringMeta(meta, "argument_hint"),
		AllowedTools: stringSliceMeta(meta, "allowed_tools"),
		Model:        core.ModelID(stringMeta(meta, "model")),
		Body:         strings.TrimSpace(body),
		Path:         path,
	}
	return def, nil
}

func (m *Manager) Definitions() []Definition {
	out := make([]Definition, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.items[name])
	}
	return out
}

func (m *Manager) Names() []string {
	return append([]string(nil), m.order...)
}

func (m *Manager) Get(name string) (Definition, bool) {
	def, ok := m.items[name]
	return def, ok
}

func (m *Manager) Loaded() bool { return m.loaded }

func parseFrontmatter(input string) map[string]interface{} {
	out := map[string]interface{}{}
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
				items = append(items, trimYAMLScalar(strings.TrimSpace(strings.TrimPrefix(next, "- "))))
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
				items = append(items, trimYAMLScalar(strings.TrimSpace(part)))
			}
			out[key] = items
			continue
		}
		out[key] = trimYAMLScalar(val)
	}
	return out
}

func stringMeta(meta map[string]interface{}, key string) string {
	s, _ := meta[key].(string)
	return s
}

func stringSliceMeta(meta map[string]interface{}, key string) []string {
	switch vals := meta[key].(type) {
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

func trimYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}
