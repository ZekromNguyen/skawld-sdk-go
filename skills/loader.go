package skills

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
	doc := frontmatter.ParseDocument(string(raw))
	def := Definition{
		Name:         doc.Metadata.String("name"),
		Description:  doc.Metadata.String("description"),
		WhenToUse:    doc.Metadata.String("when_to_use"),
		ArgumentHint: doc.Metadata.String("argument_hint"),
		AllowedTools: doc.Metadata.Strings("allowed_tools"),
		Model:        core.ModelID(doc.Metadata.String("model")),
		Body:         strings.TrimSpace(doc.Body),
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
