package sessions

import (
	"context"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// MigrateLegacySessionIdentity assigns trusted tenant and actor ownership to
// one legacy session. The authenticated context must exactly match the target
// principal, and conflicting existing ownership is never overwritten.
func MigrateLegacySessionIdentity(
	ctx context.Context,
	store core.SessionStore,
	sessionID string,
	principal core.Principal,
) (core.SessionRecord, error) {
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return core.SessionRecord{}, core.NewConfigError(
			"session identity migration requires a store and session id",
		)
	}
	authenticated, ok := core.PrincipalFromContext(ctx)
	if !ok || !authenticated.Authenticated() ||
		!principal.Authenticated() ||
		authenticated.TenantID != principal.TenantID ||
		authenticated.ActorID != principal.ActorID {
		return core.SessionRecord{}, core.NewPermissionError(
			"session identity migration requires matching authenticated tenant and actor",
		)
	}
	record, exists, err := store.Load(ctx, sessionID)
	if err != nil {
		return core.SessionRecord{}, err
	}
	if !exists {
		return core.SessionRecord{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "session not found",
		}
	}
	bound, compatible := core.BindPrincipalToSessionMeta(
		record.Meta, principal,
	)
	if !compatible {
		return core.SessionRecord{}, core.NewPermissionError(
			"session already has conflicting identity",
		)
	}
	updated, err := store.UpdateMeta(ctx, sessionID, bound)
	if err != nil {
		return core.SessionRecord{}, err
	}
	if !core.CanAccessSessionStrict(principal, updated.Meta) {
		return core.SessionRecord{}, &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "session identity migration did not persist strict ownership",
		}
	}
	return updated, nil
}
