package evaluation

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type ExtractorStore interface {
	SaveExtractorReport(context.Context, ExtractorReport) error
	GetExtractorReport(context.Context, string) (ExtractorReport, bool, error)
	ListExtractorReports(context.Context, string) ([]ExtractorReport, error)
}

type MemoryExtractorStore struct {
	mu      sync.RWMutex
	reports map[string]ExtractorReport
}

func NewMemoryExtractorStore() *MemoryExtractorStore {
	return &MemoryExtractorStore{reports: make(map[string]ExtractorReport)}
}

func (s *MemoryExtractorStore) SaveExtractorReport(ctx context.Context, report ExtractorReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return err
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return core.NewPermissionError("extractor evaluation report belongs to another tenant")
	}
	cloned, err := cloneExtractorReport(report)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.reports[report.ID]; exists {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "extractor evaluation report already exists"}
	}
	s.reports[report.ID] = cloned
	return nil
}

func (s *MemoryExtractorStore) GetExtractorReport(
	ctx context.Context,
	reportID string,
) (ExtractorReport, bool, error) {
	if err := ctx.Err(); err != nil {
		return ExtractorReport{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	report, ok := s.reports[reportID]
	if !ok {
		return ExtractorReport{}, false, nil
	}
	if !tenantAllowed(ctx, report.TenantID) {
		return ExtractorReport{}, false, core.NewPermissionError("extractor evaluation report belongs to another tenant")
	}
	cloned, err := cloneExtractorReport(report)
	return cloned, true, err
}

func (s *MemoryExtractorStore) ListExtractorReports(
	ctx context.Context,
	suiteName string,
) ([]ExtractorReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]ExtractorReport, 0)
	for _, report := range s.reports {
		if suiteName != "" && report.SuiteName != suiteName {
			continue
		}
		if !tenantAllowed(ctx, report.TenantID) {
			continue
		}
		cloned, err := cloneExtractorReport(report)
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

func cloneExtractorReport(report ExtractorReport) (ExtractorReport, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return ExtractorReport{}, err
	}
	var cloned ExtractorReport
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return ExtractorReport{}, err
	}
	return cloned, nil
}
