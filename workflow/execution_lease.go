package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// ExecutionClaim is a fencing lease over one non-terminal execution. Token is
// monotonically increased each time ownership changes, preventing a stale
// worker from checkpointing after another worker has taken over.
type ExecutionClaim struct {
	Execution  Execution
	Owner      string
	Token      int64
	LeaseUntil time.Time
}

type ExecutionLeaseStore interface {
	ExecutionStore
	ClaimExecution(
		context.Context, string, string, time.Duration,
	) (ExecutionClaim, bool, error)
	RenewExecution(
		context.Context, ExecutionClaim, time.Duration,
	) (ExecutionClaim, error)
	ReleaseExecution(context.Context, ExecutionClaim) error
}

// ReadyExecutionClaimRequest describes a bounded worker-queue claim. Stores
// must exclude every live lease before applying Limit so busy rows cannot
// starve older ready work.
type ReadyExecutionClaimRequest struct {
	Statuses []ExecutionStatus
	Owner    string
	Duration time.Duration
	Limit    int
}

// ReadyExecutionClaimer atomically selects and leases ready executions.
// Implementations should prefer the oldest ready rows to provide bounded
// fairness across repeated coordinator polls.
type ReadyExecutionClaimer interface {
	ExecutionLeaseStore
	ClaimReadyExecutions(
		context.Context,
		ReadyExecutionClaimRequest,
	) ([]ExecutionClaim, error)
}

type executionClaimContextKey struct{}

func WithExecutionClaim(
	ctx context.Context,
	claim ExecutionClaim,
) context.Context {
	return context.WithValue(ctx, executionClaimContextKey{}, claim)
}

func ExecutionClaimFromContext(
	ctx context.Context,
) (ExecutionClaim, bool) {
	claim, ok := ctx.Value(executionClaimContextKey{}).(ExecutionClaim)
	return claim, ok
}

type memoryExecutionLease struct {
	owner string
	token int64
	until time.Time
}

func (s *MemoryExecutionStore) ClaimExecution(
	ctx context.Context,
	executionID string,
	owner string,
	duration time.Duration,
) (ExecutionClaim, bool, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionClaim{}, false, err
	}
	if err := validateLeaseRequest(owner, duration); err != nil {
		return ExecutionClaim{}, false, err
	}
	principal, err := executionWorkerPrincipal(ctx)
	if err != nil {
		return ExecutionClaim{}, false, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, exists := s.items[executionID]
	if !exists {
		return ExecutionClaim{}, false, nil
	}
	if execution.Principal.TenantID != principal.TenantID {
		return ExecutionClaim{}, false, core.NewPermissionError(
			"workflow execution belongs to another tenant",
		)
	}
	if executionTerminal(execution.Status) {
		return ExecutionClaim{}, false, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "terminal workflow execution cannot be claimed",
		}
	}
	if s.leases == nil {
		s.leases = make(map[string]memoryExecutionLease)
	}
	lease := s.leases[executionID]
	if lease.owner != "" && now.Before(lease.until) &&
		lease.owner != owner {
		return ExecutionClaim{}, false, nil
	}
	if lease.owner != owner || !now.Before(lease.until) {
		lease.token = atomic.AddInt64(&s.nextFence, 1)
	}
	lease.owner = owner
	lease.until = now.Add(duration)
	s.leases[executionID] = lease
	cloned, err := cloneExecution(execution)
	if err != nil {
		return ExecutionClaim{}, false, err
	}
	return ExecutionClaim{
		Execution: cloned, Owner: owner,
		Token: lease.token, LeaseUntil: lease.until,
	}, true, nil
}

func (s *MemoryExecutionStore) ClaimReadyExecutions(
	ctx context.Context,
	request ReadyExecutionClaimRequest,
) ([]ExecutionClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateReadyExecutionClaimRequest(request); err != nil {
		return nil, err
	}
	principal, err := executionWorkerPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make(map[ExecutionStatus]struct{}, len(request.Statuses))
	for _, status := range request.Statuses {
		statuses[status] = struct{}{}
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ready := make([]Execution, 0)
	for _, execution := range s.items {
		if execution.Principal.TenantID != principal.TenantID {
			continue
		}
		if _, included := statuses[execution.Status]; !included {
			continue
		}
		lease := s.leases[execution.ID]
		if lease.owner != "" && now.Before(lease.until) {
			continue
		}
		ready = append(ready, execution)
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].UpdatedAt.Equal(ready[j].UpdatedAt) {
			return ready[i].ID < ready[j].ID
		}
		return ready[i].UpdatedAt.Before(ready[j].UpdatedAt)
	})
	if len(ready) > request.Limit {
		ready = ready[:request.Limit]
	}
	if s.leases == nil {
		s.leases = make(map[string]memoryExecutionLease)
	}
	claims := make([]ExecutionClaim, 0, len(ready))
	for _, execution := range ready {
		lease := s.leases[execution.ID]
		lease.owner = request.Owner
		lease.token = atomic.AddInt64(&s.nextFence, 1)
		lease.until = now.Add(request.Duration)
		s.leases[execution.ID] = lease
		cloned, err := cloneExecution(execution)
		if err != nil {
			return nil, err
		}
		claims = append(claims, ExecutionClaim{
			Execution: cloned, Owner: request.Owner,
			Token: lease.token, LeaseUntil: lease.until,
		})
	}
	return claims, nil
}

func (s *MemoryExecutionStore) RenewExecution(
	ctx context.Context,
	claim ExecutionClaim,
	duration time.Duration,
) (ExecutionClaim, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionClaim{}, err
	}
	if err := validateLeaseRequest(claim.Owner, duration); err != nil {
		return ExecutionClaim{}, err
	}
	principal, err := executionWorkerPrincipal(ctx)
	if err != nil {
		return ExecutionClaim{}, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, exists := s.items[claim.Execution.ID]
	lease := s.leases[claim.Execution.ID]
	if !exists || execution.Principal.TenantID != principal.TenantID ||
		lease.owner != claim.Owner || lease.token != claim.Token ||
		!now.Before(lease.until) {
		return ExecutionClaim{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution lease was lost",
		}
	}
	lease.until = now.Add(duration)
	s.leases[claim.Execution.ID] = lease
	cloned, err := cloneExecution(execution)
	if err != nil {
		return ExecutionClaim{}, err
	}
	claim.Execution = cloned
	claim.LeaseUntil = lease.until
	return claim, nil
}

func (s *MemoryExecutionStore) ReleaseExecution(
	ctx context.Context,
	claim ExecutionClaim,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, err := executionWorkerPrincipal(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, exists := s.items[claim.Execution.ID]
	if !exists {
		return nil
	}
	if execution.Principal.TenantID != principal.TenantID {
		return core.NewPermissionError(
			"workflow execution belongs to another tenant",
		)
	}
	lease := s.leases[claim.Execution.ID]
	if lease.owner != claim.Owner || lease.token != claim.Token ||
		!s.now().Before(lease.until) {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution lease was lost",
		}
	}
	delete(s.leases, claim.Execution.ID)
	return nil
}

func validateLeaseRequest(owner string, duration time.Duration) error {
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 256 ||
		strings.ContainsAny(owner, "\r\n\x00") {
		return core.NewConfigError("workflow execution lease owner is invalid")
	}
	if duration < time.Second || duration > 24*time.Hour {
		return core.NewConfigError(
			"workflow execution lease duration must be between one second and 24 hours",
		)
	}
	return nil
}

func validateReadyExecutionClaimRequest(
	request ReadyExecutionClaimRequest,
) error {
	if err := validateLeaseRequest(
		request.Owner, request.Duration,
	); err != nil {
		return err
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return core.NewConfigError(
			"workflow ready-claim limit must be between 1 and 1000",
		)
	}
	if len(request.Statuses) == 0 {
		return core.NewConfigError(
			"workflow ready claim requires at least one status",
		)
	}
	for _, status := range request.Statuses {
		if status != ExecutionRunning &&
			status != ExecutionAwaitingApproval {
			return core.NewConfigError(
				"workflow ready claim status is invalid",
			)
		}
	}
	return nil
}

func executionWorkerPrincipal(
	ctx context.Context,
) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || !principal.Authenticated() {
		return core.Principal{}, core.NewPermissionError(
			"workflow execution leasing requires authenticated tenant and actor identities",
		)
	}
	return principal, nil
}

func requireExecutionClaim(
	ctx context.Context,
	executionID string,
	lease memoryExecutionLease,
	now time.Time,
) error {
	if lease.owner == "" {
		return nil
	}
	if !now.Before(lease.until) {
		return &core.SkawldError{
			Kind: core.ErrorConflict,
			Message: fmt.Sprintf(
				"workflow execution %q lease expired", executionID,
			),
		}
	}
	claim, ok := ExecutionClaimFromContext(ctx)
	if !ok || claim.Execution.ID != executionID ||
		claim.Owner != lease.owner || claim.Token != lease.token {
		return &core.SkawldError{
			Kind: core.ErrorConflict,
			Message: fmt.Sprintf(
				"workflow execution %q is leased by another worker",
				executionID,
			),
		}
	}
	return nil
}

var _ ExecutionLeaseStore = (*MemoryExecutionStore)(nil)
var _ ReadyExecutionClaimer = (*MemoryExecutionStore)(nil)
