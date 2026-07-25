package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type Store interface {
	SaveCandidate(context.Context, Version) (Version, error)
	Publish(context.Context, string, int, core.Principal) (Version, error)
	Get(context.Context, string, int) (Version, bool, error)
	Published(context.Context, string) (Version, bool, error)
	ListVersions(context.Context, string) ([]Version, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	versions map[string]map[int]Version
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{versions: make(map[string]map[int]Version)}
}

func (s *MemoryStore) SaveCandidate(ctx context.Context, version Version) (Version, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, err
	}
	version.Status = VersionCandidate
	principal, _ := core.PrincipalFromContext(ctx)
	if version.Workflow.TenantID == "" && principal.TenantID != "" {
		version.Workflow.TenantID = principal.TenantID
	}
	if version.Workflow.TenantID != "" && version.Workflow.TenantID != principal.TenantID {
		return Version{}, core.NewPermissionError("workflow belongs to another tenant")
	}
	if version.SchemaVersion == "" {
		version.SchemaVersion = SchemaVersion
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now().UTC()
	}
	if err := version.Validate(); err != nil {
		return Version{}, err
	}
	cloned, err := cloneVersion(version)
	if err != nil {
		return Version{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versions[version.Workflow.ID] == nil {
		s.versions[version.Workflow.ID] = make(map[int]Version)
	}
	if _, exists := s.versions[version.Workflow.ID][version.Version]; exists {
		return Version{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "workflow version already exists"}
	}
	s.versions[version.Workflow.ID][version.Version] = cloned
	return cloneVersion(cloned)
}

func (s *MemoryStore) Publish(ctx context.Context, workflowID string, number int, principal core.Principal) (Version, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.versions[workflowID]
	version, ok := versions[number]
	if !ok {
		return Version{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: "workflow version not found"}
	}
	if version.Status != VersionCandidate {
		return Version{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "only candidate workflows can be published"}
	}
	if version.Workflow.TenantID != "" && version.Workflow.TenantID != principal.TenantID {
		return Version{}, core.NewPermissionError("workflow belongs to another tenant")
	}
	for existingNumber, existing := range versions {
		if existing.Status == VersionPublished {
			existing.Status = VersionRetired
			versions[existingNumber] = existing
		}
	}
	version.Status = VersionPublished
	version.PublishedAt = time.Now().UTC()
	version.PublishedBy = principal.ActorID
	versions[number] = version
	return cloneVersion(version)
}

func (s *MemoryStore) Get(ctx context.Context, workflowID string, number int) (Version, bool, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.versions[workflowID][number]
	if !ok {
		return Version{}, false, nil
	}
	principal, _ := core.PrincipalFromContext(ctx)
	if version.Workflow.TenantID != "" && version.Workflow.TenantID != principal.TenantID {
		return Version{}, false, core.NewPermissionError("workflow belongs to another tenant")
	}
	cloned, err := cloneVersion(version)
	return cloned, true, err
}

func (s *MemoryStore) Published(ctx context.Context, workflowID string) (Version, bool, error) {
	versions, err := s.ListVersions(ctx, workflowID)
	if err != nil {
		return Version{}, false, err
	}
	for index := len(versions) - 1; index >= 0; index-- {
		if versions[index].Status == VersionPublished {
			return versions[index], true, nil
		}
	}
	return Version{}, false, nil
}

func (s *MemoryStore) ListVersions(ctx context.Context, workflowID string) ([]Version, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Version, 0, len(s.versions[workflowID]))
	for _, version := range s.versions[workflowID] {
		principal, _ := core.PrincipalFromContext(ctx)
		if version.Workflow.TenantID != "" && version.Workflow.TenantID != principal.TenantID {
			continue
		}
		cloned, err := cloneVersion(version)
		if err != nil {
			return nil, err
		}
		out = append(out, cloned)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func cloneVersion(version Version) (Version, error) {
	raw, err := json.Marshal(version)
	if err != nil {
		return Version{}, fmt.Errorf("clone workflow version: %w", err)
	}
	var cloned Version
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return Version{}, fmt.Errorf("clone workflow version: %w", err)
	}
	return cloned, nil
}
