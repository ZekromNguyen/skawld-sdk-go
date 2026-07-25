package main

import (
	"context"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/permissions"
)

// permissionBroker bridges the SDK's synchronous authorization callback with
// Raven's event-driven terminal dialog.
type permissionBroker struct {
	mu          sync.Mutex
	interactive bool
	allowEdits  bool
	waiters     map[string]chan permissions.CanUseToolResponse
	early       map[string]permissions.CanUseToolResponse
}

func newPermissionBroker(interactive bool) *permissionBroker {
	return &permissionBroker{
		interactive: interactive,
		waiters:     make(map[string]chan permissions.CanUseToolResponse),
		early:       make(map[string]permissions.CanUseToolResponse),
	}
}

func (b *permissionBroker) CanUseTool(ctx context.Context, request permissions.CanUseToolRequest) (permissions.CanUseToolResponse, error) {
	b.mu.Lock()
	if !b.interactive {
		b.mu.Unlock()
		return permissions.CanUseToolResponse{Behavior: "deny", Message: "interactive approval is unavailable in single-shot mode"}, nil
	}
	if b.allowEdits && request.Descriptor.Risk == core.RiskMedium && request.Descriptor.SideEffect == core.SideEffectIdempotent {
		b.mu.Unlock()
		return permissions.CanUseToolResponse{Behavior: "allow"}, nil
	}
	if response, ok := b.early[request.ToolUseID]; ok {
		delete(b.early, request.ToolUseID)
		b.mu.Unlock()
		return response, nil
	}
	waiter := make(chan permissions.CanUseToolResponse, 1)
	b.waiters[request.ToolUseID] = waiter
	b.mu.Unlock()

	select {
	case response := <-waiter:
		return response, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiters, request.ToolUseID)
		b.mu.Unlock()
		return permissions.CanUseToolResponse{}, ctx.Err()
	}
}

func (b *permissionBroker) Resolve(toolUseID string, response permissions.CanUseToolResponse) {
	b.mu.Lock()
	if waiter, ok := b.waiters[toolUseID]; ok {
		delete(b.waiters, toolUseID)
		b.mu.Unlock()
		waiter <- response
		return
	}
	b.early[toolUseID] = response
	b.mu.Unlock()
}

func (b *permissionBroker) EnableEdits() {
	b.mu.Lock()
	b.allowEdits = true
	b.mu.Unlock()
}
