package tools

import (
	"fmt"
	"sort"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// Registry stores tools by name. Registry methods are safe for concurrent use;
// cloning a registry transfers tool membership without copying the tool values
// themselves.
type Registry struct {
	mu    sync.RWMutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[name]; exists {
		return core.NewConfigError(fmt.Sprintf("tool %q already registered", name))
	}
	r.items[name] = tool
	r.order = append(r.order, name)
	return nil
}

func (r *Registry) Get(name string) (core.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.items[name]
	return t, ok
}

func (r *Registry) List() []core.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]core.Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.items[name])
	}
	return out
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.items))
	for name := range r.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Schemas() []core.ToolSchema {
	tools := r.List()
	out := make([]core.ToolSchema, 0, len(tools))
	for _, t := range tools {
		out = append(out, core.ToolSchema{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}

func (r *Registry) Clone() *Registry {
	if r == nil {
		return NewRegistry()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := NewRegistry()
	out.order = append(out.order, r.order...)
	for name, tool := range r.items {
		out.items[name] = tool
	}
	return out
}

// Unregister removes a tool by name. Returns true if the tool was present.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[name]; !exists {
		return false
	}
	delete(r.items, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return true
}

func DefaultTools() *Registry {
	r := NewRegistry()
	_ = r.Register(ReadTool{})
	_ = r.Register(RepoMapTool{})
	_ = r.Register(WriteTool{})
	_ = r.Register(EditTool{})
	_ = r.Register(BashTool{})
	_ = r.Register(GlobTool{})
	_ = r.Register(GrepTool{})
	_ = r.Register(WebSearchTool{})
	_ = r.Register(WebFetchTool{})
	_ = r.Register(TaskCreateTool{})
	_ = r.Register(TaskListTool{})
	_ = r.Register(TaskGetTool{})
	_ = r.Register(TaskUpdateTool{})
	_ = r.Register(ProcessTool{})
	_ = r.Register(MemoryReadTool{})
	_ = r.Register(MemoryWriteTool{})
	_ = r.Register(MemorySearchTool{})
	_ = r.Register(SessionSearchTool{})
	_ = r.Register(SubagentTool{})
	_ = r.Register(BrowserNavigateTool{})
	_ = r.Register(BrowserSnapshotTool{})
	_ = r.Register(BrowserVisionTool{})
	_ = r.Register(CronCreateTool{})
	_ = r.Register(CronListTool{})
	_ = r.Register(CronDeleteTool{})
	_ = r.Register(XSearchTool{})
	_ = r.Register(VisionAnalyzeTool{})
	_ = r.Register(ImageGenerateTool{})
	_ = r.Register(TextToSpeechTool{})
	return r
}
