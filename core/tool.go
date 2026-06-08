package core

import "context"

type FileReadTracker interface {
	MarkRead(absPath string)
	HasRead(absPath string) bool
}

// ToolContext is passed to Tool.Execute for one tool invocation. It is safe to
// retain only for the duration of Execute; tools should honor Context
// cancellation and use Filesystem when resolving local paths.
type ToolContext struct {
	Context         context.Context
	CWD             string
	Filesystem      FilesystemResolver
	FileReadTracker FileReadTracker
	Observer        Observer
	SessionID       string
	RunID           string
	SessionStore    SessionStore
	Emit            func(Event)
	InvokeSkill     func(context.Context, SkillInvocation) (ToolResult, error)
	RunSubagent     func(context.Context, SubagentInvocation) (ToolResult, error)
}

// FilesystemResolveMode describes the kind of filesystem access a built-in
// tool is resolving.
type FilesystemResolveMode string

const (
	FilesystemResolveRead   FilesystemResolveMode = "read"
	FilesystemResolveWrite  FilesystemResolveMode = "write"
	FilesystemResolveSearch FilesystemResolveMode = "search"
)

// FilesystemResolver resolves and authorizes tool paths for built-in
// filesystem tools.
type FilesystemResolver interface {
	Resolve(cwd, raw string, mode FilesystemResolveMode) (string, error)
}

type SkillInvocation struct {
	Name      string
	Arguments string
}

type SubagentInvocation struct {
	Name string
	Task string
}

type ToolResult struct {
	Content interface{}
	Summary string
	IsError bool
}

// Tool is the extension contract for model-callable tools. Tool
// implementations registered in a shared Registry can be invoked concurrently
// when ParallelSafe returns true, so any mutable tool state must be protected or
// avoided.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]interface{}
	Scope() ToolScope
	ParallelSafe() bool
	Validate(raw map[string]interface{}) (map[string]interface{}, error)
	Execute(input map[string]interface{}, ctx ToolContext) (ToolResult, error)
	Summarize(input map[string]interface{}) string
}
