package tools

// Toolset groups tools into a named, enableable category. Toolsets let callers
// quickly toggle entire groups of tools (e.g., disable all "terminal" tools in
// restricted environments).
type Toolset struct {
	Name        string   // unique identifier: "file", "terminal", "search", "web", "task", "safe"
	Label       string   // human-readable: "File Operations", "Terminal", etc.
	Description string   // one-line summary
	Tools       []string // tool names in this set
}

// BuiltinToolsets defines the standard tool groupings.
var BuiltinToolsets = []Toolset{
	{
		Name:        "file",
		Label:       "File Operations",
		Description: "Read, write, edit, and search files",
		Tools:       []string{"Read", "Write", "Edit", "Glob"},
	},
	{
		Name:        "terminal",
		Label:       "Terminal",
		Description: "Execute shell commands and manage background processes",
		Tools:       []string{"Bash", "Process"},
	},
	{
		Name:        "search",
		Label:       "Search",
		Description: "Search file contents and names",
		Tools:       []string{"Grep", "Glob"},
	},
	{
		Name:        "web",
		Label:       "Web",
		Description: "Search the web and fetch URLs",
		Tools:       []string{"WebSearch", "WebFetch"},
	},
	{
		Name:        "task",
		Label:       "Tasks",
		Description: "Create and manage tasks",
		Tools:       []string{"TaskCreate", "TaskList", "TaskGet", "TaskUpdate"},
	},
	{
		Name:        "memory",
		Label:       "Memory",
		Description: "Read, write, and search persistent memories and prior sessions",
		Tools:       []string{"MemoryRead", "MemoryWrite", "MemorySearch", "SessionSearch"},
	},
	{
		Name:        "subagent",
		Label:       "Subagents",
		Description: "Delegate work to child agents",
		Tools:       []string{"Subagent"},
	},
	{
		Name:        "browser",
		Label:       "Browser",
		Description: "Navigate, inspect, and screenshot web pages in a headless browser",
		Tools:       []string{"BrowserNavigate", "BrowserSnapshot", "BrowserVision"},
	},
	{
		Name:        "cron",
		Label:       "Cron Jobs",
		Description: "Schedule prompts to fire on a recurring cron schedule",
		Tools:       []string{"CronCreate", "CronList", "CronDelete"},
	},
	{
		Name:        "xsearch",
		Label:       "X Search",
		Description: "Search X/Twitter posts via the xAI API",
		Tools:       []string{"XSearch"},
	},
	{
		Name:        "media",
		Label:       "Media",
		Description: "Analyze images, generate images via DALL-E, and convert text to speech",
		Tools:       []string{"VisionAnalyze", "ImageGenerate", "TextToSpeech"},
	},
	{
		Name:        "safe",
		Label:       "Safe (read-only)",
		Description: "Read-only subset — no writes, no execution",
		Tools:       []string{"Read", "Glob", "Grep", "TaskList", "TaskGet", "MemoryRead", "MemorySearch", "SessionSearch", "CronList", "XSearch", "VisionAnalyze"},
	},
}

// ToolsetState tracks which toolsets are enabled on a Registry.
type ToolsetState struct {
	enabled map[string]bool // toolset name → enabled
}

// NewToolsetState creates a ToolsetState with all builtin toolsets enabled.
func NewToolsetState() *ToolsetState {
	ts := &ToolsetState{enabled: make(map[string]bool)}
	for _, t := range BuiltinToolsets {
		ts.enabled[t.Name] = true
	}
	return ts
}

// Enable activates a toolset by name. Returns the list of tool names
// that belong to the toolset (so callers can re-register them if needed).
// If the toolset is unknown, returns nil.
func (ts *ToolsetState) Enable(name string) []string {
	for _, t := range BuiltinToolsets {
		if t.Name == name {
			ts.enabled[name] = true
			return t.Tools
		}
	}
	return nil
}

// Disable deactivates a toolset by name. Returns the list of tool names
// that belong to the toolset (so callers can unregister them).
// If the toolset is unknown, returns nil.
func (ts *ToolsetState) Disable(name string) []string {
	for _, t := range BuiltinToolsets {
		if t.Name == name {
			ts.enabled[name] = false
			return t.Tools
		}
	}
	return nil
}

// IsEnabled reports whether a toolset is currently enabled.
func (ts *ToolsetState) IsEnabled(name string) bool {
	enabled, ok := ts.enabled[name]
	return ok && enabled
}

// EnabledToolsets returns the names of all enabled toolsets.
func (ts *ToolsetState) EnabledToolsets() []string {
	var names []string
	for _, t := range BuiltinToolsets {
		if ts.enabled[t.Name] {
			names = append(names, t.Name)
		}
	}
	return names
}

// DisabledToolsets returns the names of all disabled toolsets.
func (ts *ToolsetState) DisabledToolsets() []string {
	var names []string
	for _, t := range BuiltinToolsets {
		if !ts.enabled[t.Name] {
			names = append(names, t.Name)
		}
	}
	return names
}

// Apply applies the toolset state to a Registry by unregistering tools
// that belong to disabled toolsets. Returns the list of unregistered tool
// names.
func (ts *ToolsetState) Apply(registry *Registry) []string {
	var removed []string
	for _, t := range BuiltinToolsets {
		if !ts.enabled[t.Name] {
			for _, toolName := range t.Tools {
				if registry.Unregister(toolName) {
					removed = append(removed, toolName)
				}
			}
		}
	}
	return removed
}

// AllToolNames returns the set of all tool names across all toolsets.
func AllToolNames() []string {
	seen := make(map[string]bool)
	var names []string
	for _, t := range BuiltinToolsets {
		for _, name := range t.Tools {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}
