package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// Route maps an application-defined task type to one workflow identity.
// Resolution is intentionally exact and deterministic; semantic/LLM routing
// belongs outside this safety boundary.
type Route struct {
	TaskType   string    `json:"task_type"`
	WorkflowID string    `json:"workflow_id"`
	TenantID   string    `json:"tenant_id,omitempty"`
	Revision   int64     `json:"revision,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
	UpdatedBy  string    `json:"updated_by,omitempty"`
}

type ResolutionRequest struct {
	TaskType   string
	WorkflowID string
}

type ResolverOptions struct {
	Store      Store
	Routes     []Route
	RouteStore RouteStore
}

type Resolver struct {
	store      Store
	routes     map[string]string
	routeStore RouteStore
}

func NewResolver(options ResolverOptions) (*Resolver, error) {
	if options.Store == nil {
		return nil, core.NewConfigError("workflow resolver requires a store")
	}
	if len(options.Routes) > 0 && options.RouteStore != nil {
		return nil, core.NewConfigError(
			"workflow resolver accepts static routes or a route store, not both",
		)
	}
	resolver := &Resolver{store: options.Store, routes: make(map[string]string, len(options.Routes))}
	resolver.routeStore = options.RouteStore
	for _, route := range options.Routes {
		taskType := strings.TrimSpace(route.TaskType)
		workflowID := strings.TrimSpace(route.WorkflowID)
		route.TaskType, route.WorkflowID = taskType, workflowID
		if err := route.Validate(); err != nil {
			return nil, err
		}
		if _, exists := resolver.routes[taskType]; exists {
			return nil, core.NewConfigError(fmt.Sprintf("duplicate workflow route %q", taskType))
		}
		resolver.routes[taskType] = workflowID
	}
	return resolver, nil
}

// Resolve returns only the currently published version. A caller may address a
// workflow directly, use an exact task route, or supply both as a consistency
// assertion.
func (r *Resolver) Resolve(
	ctx context.Context,
	request ResolutionRequest,
) (Version, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, err
	}
	taskType := strings.TrimSpace(request.TaskType)
	workflowID := strings.TrimSpace(request.WorkflowID)
	if len(taskType) > 256 || strings.ContainsAny(taskType, "\r\n\x00") ||
		len(workflowID) > 256 || strings.ContainsAny(workflowID, "\r\n\x00") {
		return Version{}, core.NewConfigError("workflow resolution identity is invalid")
	}
	if taskType == "" && workflowID == "" {
		return Version{}, core.NewConfigError("workflow resolution requires task type or workflow id")
	}
	if taskType != "" {
		routedID, exists, err := r.resolveTaskType(ctx, taskType)
		if err != nil {
			return Version{}, err
		}
		if !exists {
			return Version{}, &core.SkawldError{
				Kind: core.ErrorNotFound, Message: fmt.Sprintf("no workflow route exists for task type %q", taskType),
			}
		}
		if workflowID != "" && workflowID != routedID {
			return Version{}, &core.SkawldError{
				Kind: core.ErrorConflict, Message: "task route does not match requested workflow",
			}
		}
		workflowID = routedID
	}
	version, exists, err := r.store.Published(ctx, workflowID)
	if err != nil {
		return Version{}, err
	}
	if !exists {
		return Version{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: fmt.Sprintf("published workflow %q not found", workflowID),
		}
	}
	return version, nil
}

func (r *Resolver) resolveTaskType(
	ctx context.Context,
	taskType string,
) (string, bool, error) {
	if r.routeStore != nil {
		route, exists, err := r.routeStore.Get(ctx, taskType)
		if err != nil || !exists {
			return "", exists, err
		}
		return route.WorkflowID, true, nil
	}
	workflowID, exists := r.routes[taskType]
	return workflowID, exists, nil
}
