package evaluation

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type AgentStore interface {
	SaveAgentReport(context.Context, AgentReport) error
	GetAgentReport(context.Context, string) (AgentReport, bool, error)
	ListAgentReports(context.Context, string) ([]AgentReport, error)
}

type MemoryAgentStore struct {
	mu      sync.RWMutex
	reports map[string]AgentReport
}

func NewMemoryAgentStore() *MemoryAgentStore {
	return &MemoryAgentStore{reports: make(map[string]AgentReport)}
}

func (s *MemoryAgentStore) SaveAgentReport(ctx context.Context, report AgentReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return core.NewPermissionError("agent evaluation report belongs to another tenant")
	}
	cloned, err := cloneAgentReport(report)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.reports[report.ID]; exists {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "agent evaluation report already exists"}
	}
	s.reports[report.ID] = cloned
	return nil
}

func (s *MemoryAgentStore) GetAgentReport(ctx context.Context, reportID string) (AgentReport, bool, error) {
	if err := ctx.Err(); err != nil {
		return AgentReport{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	report, ok := s.reports[reportID]
	if !ok {
		return AgentReport{}, false, nil
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return AgentReport{}, false, core.NewPermissionError("agent evaluation report belongs to another tenant")
	}
	cloned, err := cloneAgentReport(report)
	return cloned, true, err
}

func (s *MemoryAgentStore) ListAgentReports(ctx context.Context, suiteName string) ([]AgentReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]AgentReport, 0)
	for _, report := range s.reports {
		if suiteName != "" && report.SuiteName != suiteName {
			continue
		}
		if !tenantAllowed(ctx, report.TenantID) {
			continue
		}
		cloned, err := cloneAgentReport(report)
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

func cloneAgentReport(report AgentReport) (AgentReport, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return AgentReport{}, err
	}
	var cloned AgentReport
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return AgentReport{}, err
	}
	return cloned, nil
}
