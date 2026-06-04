package core

import "context"

type FileReadTracker interface {
	MarkRead(absPath string)
	HasRead(absPath string) bool
}

type ToolContext struct {
	Context         context.Context
	CWD             string
	FileReadTracker FileReadTracker
	SessionID       string
	RunID           string
	SessionStore    SessionStore
	Emit            func(Event)
}

type ToolResult struct {
	Content interface{}
	Summary string
	IsError bool
}

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
