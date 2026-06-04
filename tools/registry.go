package tools

import (
	"fmt"
	"sort"

	"github.com/skawld/skawld-sdk-go/core"
)

type Registry struct {
	items map[string]core.Tool
	order []string
}

func NewRegistry() *Registry {
	return &Registry{items: map[string]core.Tool{}}
}

func (r *Registry) Register(tool core.Tool) error {
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	name := tool.Name()
	if _, exists := r.items[name]; exists {
		return core.NewConfigError(fmt.Sprintf("tool %q already registered", name))
	}
	r.items[name] = tool
	r.order = append(r.order, name)
	return nil
}

func (r *Registry) Get(name string) (core.Tool, bool) {
	t, ok := r.items[name]
	return t, ok
}

func (r *Registry) List() []core.Tool {
	out := make([]core.Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.items[name])
	}
	return out
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.items))
	for name := range r.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Schemas() []core.ToolSchema {
	out := make([]core.ToolSchema, 0, len(r.order))
	for _, t := range r.List() {
		out = append(out, core.ToolSchema{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}

func DefaultTools() *Registry {
	r := NewRegistry()
	_ = r.Register(ReadTool{})
	_ = r.Register(WriteTool{})
	_ = r.Register(EditTool{})
	_ = r.Register(BashTool{})
	_ = r.Register(GlobTool{})
	_ = r.Register(GrepTool{})
	_ = r.Register(TaskCreateTool{})
	_ = r.Register(TaskListTool{})
	_ = r.Register(TaskGetTool{})
	_ = r.Register(TaskUpdateTool{})
	return r
}
