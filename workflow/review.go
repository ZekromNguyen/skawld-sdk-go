package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
)

type ReviewDecision string

const (
	ReviewApproved ReviewDecision = "approved"
	ReviewRejected ReviewDecision = "rejected"
)

// Review is an immutable human decision bound to the exact candidate digest.
// A later review supersedes an earlier review for publication policy, but does
// not mutate or delete the earlier audit record.
type Review struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id"`
	WorkflowID      string         `json:"workflow_id"`
	WorkflowVersion int            `json:"workflow_version"`
	CandidateDigest string         `json:"candidate_digest"`
	Decision        ReviewDecision `json:"decision"`
	ReviewedAt      time.Time      `json:"reviewed_at"`
	ReviewedBy      string         `json:"reviewed_by"`
	Reason          string         `json:"reason,omitempty"`
}

func (review Review) Validate() error {
	if strings.TrimSpace(review.ID) == "" || strings.TrimSpace(review.TenantID) == "" ||
		strings.TrimSpace(review.WorkflowID) == "" || review.WorkflowVersion < 1 ||
		strings.TrimSpace(review.CandidateDigest) == "" || review.ReviewedAt.IsZero() ||
		strings.TrimSpace(review.ReviewedBy) == "" {
		return fmt.Errorf("workflow review identity, candidate, reviewer, and timestamp are required")
	}
	switch review.Decision {
	case ReviewApproved, ReviewRejected:
	default:
		return fmt.Errorf("workflow review decision %q is invalid", review.Decision)
	}
	if len(review.Reason) > 4096 {
		return fmt.Errorf("workflow review reason exceeds 4096 bytes")
	}
	return nil
}

// Digest binds evaluations and reviews to an exact immutable workflow
// candidate document.
func Digest(version Version) (string, error) {
	raw, err := json.Marshal(version)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func NewReview(
	candidate Version,
	decision ReviewDecision,
	principal core.Principal,
	reason string,
	now time.Time,
) (Review, error) {
	if candidate.Status != VersionCandidate {
		return Review{}, core.NewConfigError("only candidate workflows can be reviewed")
	}
	if principal.TenantID == "" || principal.ActorID == "" ||
		candidate.Workflow.TenantID != principal.TenantID {
		return Review{}, core.NewPermissionError("workflow review requires the candidate tenant and reviewer identity")
	}
	digest, err := Digest(candidate)
	if err != nil {
		return Review{}, err
	}
	reviewID, err := id.New()
	if err != nil {
		return Review{}, err
	}
	review := Review{
		ID: reviewID, TenantID: principal.TenantID, WorkflowID: candidate.Workflow.ID,
		WorkflowVersion: candidate.Version, CandidateDigest: digest, Decision: decision,
		ReviewedAt: now.UTC(), ReviewedBy: principal.ActorID, Reason: reason,
	}
	if err := review.Validate(); err != nil {
		return Review{}, err
	}
	return review, nil
}

type ReviewStore interface {
	Save(context.Context, Review) error
	Get(context.Context, string) (Review, bool, error)
	List(context.Context, string, int) ([]Review, error)
}

type MemoryReviewStore struct {
	mu      sync.RWMutex
	reviews map[string]Review
}

func NewMemoryReviewStore() *MemoryReviewStore {
	return &MemoryReviewStore{reviews: make(map[string]Review)}
}

func (s *MemoryReviewStore) Save(ctx context.Context, review Review) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := review.Validate(); err != nil {
		return err
	}
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID != review.TenantID || principal.ActorID != review.ReviewedBy {
		return core.NewPermissionError("workflow review identity does not match authenticated reviewer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.reviews[review.ID]; exists {
		return &core.SkawldError{Kind: core.ErrorConflict, Message: "workflow review already exists"}
	}
	s.reviews[review.ID] = review
	return nil
}

func (s *MemoryReviewStore) Get(ctx context.Context, reviewID string) (Review, bool, error) {
	if err := ctx.Err(); err != nil {
		return Review{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	review, exists := s.reviews[reviewID]
	if !exists {
		return Review{}, false, nil
	}
	if !reviewTenantAllowed(ctx, review.TenantID) {
		return Review{}, false, core.NewPermissionError("workflow review belongs to another tenant")
	}
	return review, true, nil
}

func (s *MemoryReviewStore) List(
	ctx context.Context,
	workflowID string,
	version int,
) ([]Review, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Review, 0)
	for _, review := range s.reviews {
		if workflowID != "" && review.WorkflowID != workflowID {
			continue
		}
		if version > 0 && review.WorkflowVersion != version {
			continue
		}
		if reviewTenantAllowed(ctx, review.TenantID) {
			output = append(output, review)
		}
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].ReviewedAt.Equal(output[j].ReviewedAt) {
			return output[i].ID < output[j].ID
		}
		return output[i].ReviewedAt.Before(output[j].ReviewedAt)
	})
	return output, nil
}

func reviewTenantAllowed(ctx context.Context, tenantID string) bool {
	principal, ok := core.PrincipalFromContext(ctx)
	return ok && principal.TenantID != "" && principal.TenantID == tenantID
}
