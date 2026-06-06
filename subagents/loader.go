package subagents

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
	SystemPrompt string
	Tools        []string
	Model        core.ModelID
	Body         string
	Path         string
	BuiltIn      bool
}

func DefaultDefinition() Definition {
	return Definition{
		Name:         "default",
		Description:  "General-purpose subagent for isolated investigation or delegated work.",
		SystemPrompt: "You are a focused child agent. Complete the delegated task and report the useful result concisely.",
		Tools:        []string{"*"},
		BuiltIn:      true,
	}
}

type Registry struct {
	dir    string
	loaded bool
	order  []string
	items  map[string]Definition
}

func NewRegistry(dir string) *Registry {
	return &Registry{dir: dir, items: map[string]Definition{}}
}

func (r *Registry) Load() error {
	if r.loaded {
		return nil
	}
	r.loaded = true
	r.add(DefaultDefinition())
	if strings.TrimSpace(r.dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(r.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			continue
		}
		path := filepath.Join(r.dir, entry.Name())
		def, err := LoadFile(path)
		if err != nil {
			return err
		}
		if def.Name == "" {
			def.Name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		if _, exists := r.items[def.Name]; exists {
			return fmt.Errorf("duplicate subagent %q", def.Name)
		}
		r.add(def)
	}
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
		SystemPrompt: stringMeta(meta, "system_prompt"),
		Tools:        stringSliceMeta(meta, "tools"),
		Model:        core.ModelID(stringMeta(meta, "model")),
		Body:         strings.TrimSpace(body),
		Path:         path,
	}
	if def.SystemPrompt == "" {
		def.SystemPrompt = def.Body
	}
	if len(def.Tools) == 0 {
		def.Tools = []string{"*"}
	}
	return def, nil
}

func (r *Registry) Definitions() []Definition {
	out := make([]Definition, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.items[name])
	}
	return out
}

func (r *Registry) Names() []string {
	return append([]string(nil), r.order...)
}

func (r *Registry) Get(name string) (Definition, bool) {
	def, ok := r.items[name]
	return def, ok
}

func (r *Registry) Loaded() bool { return r.loaded }

func (r *Registry) add(def Definition) {
	r.items[def.Name] = def
	r.order = append(r.order, def.Name)
	sort.Strings(r.order)
}

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
