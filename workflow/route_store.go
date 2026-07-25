package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// RouteStore persists exact task-type mappings. Tenant scope always comes from
// the authenticated principal, never from lookup input.
type RouteStore interface {
	Save(context.Context, Route) (Route, error)
	Get(context.Context, string) (Route, bool, error)
	List(context.Context) ([]Route, error)
	Delete(context.Context, string, int64) error
}

type MemoryRouteStore struct {
	mu     sync.RWMutex
	routes map[string]Route
	now    func() time.Time
}

func NewMemoryRouteStore() *MemoryRouteStore {
	return &MemoryRouteStore{
		routes: make(map[string]Route),
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (s *MemoryRouteStore) Save(ctx context.Context, route Route) (Route, error) {
	if err := ctx.Err(); err != nil {
		return Route{}, err
	}
	principal, err := routePrincipal(ctx)
	if err != nil {
		return Route{}, err
	}
	route.TaskType = strings.TrimSpace(route.TaskType)
	route.WorkflowID = strings.TrimSpace(route.WorkflowID)
	if err := route.Validate(); err != nil {
		return Route{}, err
	}
	if route.TenantID != "" && route.TenantID != principal.TenantID {
		return Route{}, core.NewPermissionError("workflow route belongs to another tenant")
	}
	route.TenantID = principal.TenantID
	key := routeKey(route.TenantID, route.TaskType)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.routes[key]
	switch {
	case !exists && route.Revision != 0:
		return Route{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "workflow route not found",
		}
	case exists && route.Revision == 0:
		return Route{}, &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow route already exists",
		}
	case exists && current.Revision != route.Revision:
		return Route{}, &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow route revision conflict",
		}
	}
	route.Revision++
	route.UpdatedAt = s.now()
	route.UpdatedBy = principal.ActorID
	s.routes[key] = route
	return route, nil
}

func (route Route) Validate() error {
	if route.TaskType == "" || route.WorkflowID == "" ||
		len(route.TaskType) > 256 || len(route.WorkflowID) > 256 ||
		strings.ContainsAny(route.TaskType, "\r\n\x00") ||
		strings.ContainsAny(route.WorkflowID, "\r\n\x00") {
		return core.NewConfigError("workflow route has invalid task type or workflow id")
	}
	if route.Revision < 0 {
		return core.NewConfigError("workflow route revision must not be negative")
	}
	return nil
}

func (s *MemoryRouteStore) Get(
	ctx context.Context,
	taskType string,
) (Route, bool, error) {
	if err := ctx.Err(); err != nil {
		return Route{}, false, err
	}
	principal, err := routePrincipal(ctx)
	if err != nil {
		return Route{}, false, err
	}
	taskType = strings.TrimSpace(taskType)
	if taskType == "" || len(taskType) > 256 ||
		strings.ContainsAny(taskType, "\r\n\x00") {
		return Route{}, false, core.NewConfigError("workflow route task type is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	route, exists := s.routes[routeKey(principal.TenantID, taskType)]
	return route, exists, nil
}

func (s *MemoryRouteStore) List(ctx context.Context) ([]Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	principal, err := routePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Route, 0)
	for _, route := range s.routes {
		if route.TenantID == principal.TenantID {
			output = append(output, route)
		}
	}
	sort.Slice(output, func(i, j int) bool {
		return output[i].TaskType < output[j].TaskType
	})
	return output, nil
}

func (s *MemoryRouteStore) Delete(
	ctx context.Context,
	taskType string,
	revision int64,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, err := routePrincipal(ctx)
	if err != nil {
		return err
	}
	taskType = strings.TrimSpace(taskType)
	if taskType == "" || len(taskType) > 256 ||
		strings.ContainsAny(taskType, "\r\n\x00") || revision < 1 {
		return core.NewConfigError("workflow route deletion requires task type and revision")
	}
	key := routeKey(principal.TenantID, taskType)
	s.mu.Lock()
	defer s.mu.Unlock()
	route, exists := s.routes[key]
	if !exists {
		return &core.SkawldError{Kind: core.ErrorNotFound, Message: "workflow route not found"}
	}
	if route.Revision != revision {
		return &core.SkawldError{
			Kind: core.ErrorConflict, Message: "workflow route revision conflict",
		}
	}
	delete(s.routes, key)
	return nil
}

func routePrincipal(ctx context.Context) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" || principal.ActorID == "" {
		return core.Principal{}, core.NewPermissionError(
			"workflow route storage requires an authenticated actor",
		)
	}
	return principal, nil
}

func routeKey(tenantID, taskType string) string {
	return fmt.Sprintf("%d:%s%s", len(tenantID), tenantID, taskType)
}
