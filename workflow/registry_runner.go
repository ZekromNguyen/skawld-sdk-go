package workflow

import (
	"context"
	"fmt"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

// RegistryRunner adapts the existing SDK tool registry to deterministic
// workflows. Policy and approval are deliberately enforced by Executor before
// this runner is called.
type RegistryRunner struct {
	Registry        *tools.Registry
	CWD             string
	Filesystem      core.FilesystemResolver
	SessionID       string
	SessionStore    core.SessionStore
	Observer        core.Observer
	FileReadTracker core.FileReadTracker
}

func (r RegistryRunner) Describe(ctx context.Context, name string) (core.ToolDescriptor, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolDescriptor{}, false, err
	}
	if r.Registry == nil {
		return core.ToolDescriptor{}, false, core.NewConfigError("tool registry is nil")
	}
	tool, ok := r.Registry.Get(name)
	if !ok {
		return core.ToolDescriptor{}, false, nil
	}
	return core.DescribeTool(tool), true, nil
}

func (r RegistryRunner) ToolCatalogFingerprint(
	ctx context.Context,
	names []string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return tools.CatalogFingerprint(r.Registry, names)
}

func (r RegistryRunner) Execute(ctx context.Context, name string, input map[string]interface{}, idempotencyKey string) (ToolResult, error) {
	if r.Registry == nil {
		return ToolResult{}, core.NewConfigError("tool registry is nil")
	}
	tool, ok := r.Registry.Get(name)
	if !ok {
		return ToolResult{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: fmt.Sprintf("tool %q is not registered", name)}
	}
	validated, err := tool.Validate(input)
	if err != nil {
		return ToolResult{}, err
	}
	principal, _ := core.PrincipalFromContext(ctx)
	toolContext := core.ToolContext{
		Context: ctx, CWD: r.CWD, Filesystem: r.Filesystem, FileReadTracker: r.FileReadTracker,
		Observer: r.Observer, Principal: principal, SessionID: r.SessionID, SessionStore: r.SessionStore,
	}
	var result core.ToolResult
	if idempotencyKey != "" {
		idempotent, ok := tool.(core.IdempotentTool)
		if !ok {
			return ToolResult{}, core.NewToolExecutionError(name, "tool does not implement idempotent execution")
		}
		result, err = idempotent.ExecuteIdempotent(validated, idempotencyKey, toolContext)
	} else {
		result, err = tool.Execute(validated, toolContext)
	}
	if err != nil {
		return ToolResult{}, err
	}
	if result.IsError {
		return ToolResult{Output: result.Content}, core.NewToolExecutionError(name, fmt.Sprint(result.Content))
	}
	return ToolResult{Output: result.Content}, nil
}
