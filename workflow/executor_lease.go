package workflow

import (
	"context"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func (e *Executor) acquireExecution(
	ctx context.Context,
	checkpoint Execution,
) (context.Context, Execution, func(), error) {
	if existing, ok := ExecutionClaimFromContext(ctx); ok &&
		existing.Execution.ID == checkpoint.ID {
		store, supported := e.executions.(ExecutionLeaseStore)
		if !supported {
			return ctx, checkpoint, func() {}, &ExecutionError{
				Kind:    core.ErrorConfig,
				Message: "workflow execution store does not support leases",
			}
		}
		return e.maintainExecutionClaim(ctx, store, existing)
	}
	if e.workerID == "" {
		if e.requireLease {
			return ctx, checkpoint, func() {}, &ExecutionError{
				Kind:    core.ErrorConfig,
				Message: "workflow execution lease is required",
			}
		}
		return ctx, checkpoint, func() {}, nil
	}
	store, ok := e.executions.(ExecutionLeaseStore)
	if !ok {
		return ctx, checkpoint, func() {}, &ExecutionError{
			Kind:    core.ErrorConfig,
			Message: "workflow execution store does not support leases",
		}
	}
	claim, acquired, err := store.ClaimExecution(
		ctx, checkpoint.ID, e.workerID, e.leaseDuration,
	)
	if err != nil {
		return ctx, checkpoint, func() {}, err
	}
	if !acquired {
		return ctx, checkpoint, func() {}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "workflow execution is owned by another worker",
		}
	}
	return e.maintainExecutionClaim(ctx, store, claim)
}

func (e *Executor) maintainExecutionClaim(
	ctx context.Context,
	store ExecutionLeaseStore,
	claim ExecutionClaim,
) (context.Context, Execution, func(), error) {
	leaseCtx, cancel := context.WithCancel(ctx)
	leaseCtx = WithExecutionClaim(leaseCtx, claim)
	done := make(chan struct{})
	go func() {
		defer close(done)
		interval := e.leaseDuration / 3
		if interval < 250*time.Millisecond {
			interval = 250 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, renewErr := store.RenewExecution(
					leaseCtx, claim, e.leaseDuration,
				); renewErr != nil {
					cancel()
					return
				}
			case <-leaseCtx.Done():
				return
			}
		}
	}()
	release := func() {
		cancel()
		<-done
		_ = store.ReleaseExecution(
			context.WithoutCancel(leaseCtx), claim,
		)
	}
	return leaseCtx, claim.Execution, release, nil
}
