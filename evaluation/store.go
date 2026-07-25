package evaluation

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type Store interface {
	Save(context.Context, Report) error
	Get(context.Context, string) (Report, bool, error)
	List(context.Context, string, int) ([]Report, error)
}

type MemoryStore struct {
	mu      sync.RWMutex
	reports map[string]Report
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{reports: make(map[string]Report)}
}

func (s *MemoryStore) Save(ctx context.Context, report Report) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return core.NewPermissionError("evaluation report belongs to another tenant")
	}
	cloned, err := cloneReport(report)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.reports[report.ID]; exists {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "evaluation report already exists"}
	}
	s.reports[report.ID] = cloned
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, reportID string) (Report, bool, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	report, ok := s.reports[reportID]
	if !ok {
		return Report{}, false, nil
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return Report{}, false, core.NewPermissionError("evaluation report belongs to another tenant")
	}
	cloned, err := cloneReport(report)
	return cloned, true, err
}

func (s *MemoryStore) List(ctx context.Context, workflowID string, version int) ([]Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Report, 0)
	for _, report := range s.reports {
		if workflowID != "" && report.WorkflowID != workflowID {
			continue
		}
		if version > 0 && report.WorkflowVersion != version {
			continue
		}
		if !tenantAllowed(ctx, report.TenantID) {
			continue
		}
		cloned, err := cloneReport(report)
		if err != nil {
			return nil, err
		}
		output = append(output, cloned)
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].StartedAt.Equal(output[j].StartedAt) {
			return output[i].ID < output[j].ID
		}
		return output[i].StartedAt.Before(output[j].StartedAt)
	})
	return output, nil
}

func tenantAllowed(ctx context.Context, tenantID string) bool {
	principal, ok := core.PrincipalFromContext(ctx)
	return ok && principal.TenantID != "" && principal.TenantID == tenantID
}

func cloneReport(report Report) (Report, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return Report{}, err
	}
	var cloned Report
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return Report{}, err
	}
	return cloned, nil
}
