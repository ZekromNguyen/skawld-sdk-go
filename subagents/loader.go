package subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/internal/frontmatter"
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
	doc := frontmatter.ParseDocument(string(raw))
	def := Definition{
		Name:         doc.Metadata.String("name"),
		Description:  doc.Metadata.String("description"),
		SystemPrompt: doc.Metadata.String("system_prompt"),
		Tools:        doc.Metadata.Strings("tools"),
		Model:        core.ModelID(doc.Metadata.String("model")),
		Body:         strings.TrimSpace(doc.Body),
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
