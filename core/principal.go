package core

import (
	"context"
	"strings"
)

const (
	SessionMetaTenantID = "_skawld_tenant_id"
	SessionMetaActorID  = "_skawld_actor_id"
)

// Principal identifies the human or service responsible for an SDK action.
// TenantID is the isolation boundary; ActorID is the audit identity.
type Principal struct {
	TenantID string   `json:"tenant_id,omitempty"`
	ActorID  string   `json:"actor_id,omitempty"`
	Roles    []string `json:"roles,omitempty"`
}

func (p Principal) Valid() bool {
	return strings.TrimSpace(p.TenantID) != "" || strings.TrimSpace(p.ActorID) != ""
}

// Authenticated reports whether the principal carries both identities required
// for a consequential SDK action. Valid remains intentionally broader for
// legacy session metadata that may contain only one identity.
func (p Principal) Authenticated() bool {
	return strings.TrimSpace(p.TenantID) != "" &&
		strings.TrimSpace(p.ActorID) != ""
}

type principalContextKey struct{}

// WithPrincipal attaches an authenticated principal to a context. Callers
// should construct Principal from trusted authentication data, never model
// output or user-controlled workflow fields.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok && principal.Valid()
}

func PrincipalFromSessionMeta(meta map[string]interface{}) Principal {
	if meta == nil {
		return Principal{}
	}
	tenantID, _ := meta[SessionMetaTenantID].(string)
	actorID, _ := meta[SessionMetaActorID].(string)
	return Principal{TenantID: tenantID, ActorID: actorID}
}

// CanAccessSession applies tenant isolation. Unscoped legacy sessions remain
// accessible for compatibility; a tenant-scoped session requires a matching
// authenticated tenant.
func CanAccessSession(principal Principal, meta map[string]interface{}) bool {
	owner := PrincipalFromSessionMeta(meta)
	if owner.TenantID == "" {
		return true
	}
	return principal.TenantID != "" && principal.TenantID == owner.TenantID
}

// BindPrincipalToSessionMeta returns a copy with reserved identity fields set.
// Conflicting reserved values are rejected by returning ok=false.
func BindPrincipalToSessionMeta(meta map[string]interface{}, principal Principal) (bound map[string]interface{}, ok bool) {
	bound = make(map[string]interface{}, len(meta)+2)
	for key, value := range meta {
		bound[key] = value
	}
	if !principal.Valid() {
		return bound, true
	}
	if existing, exists := bound[SessionMetaTenantID]; exists && existing != principal.TenantID {
		return nil, false
	}
	if existing, exists := bound[SessionMetaActorID]; exists && existing != principal.ActorID {
		return nil, false
	}
	if principal.TenantID != "" {
		bound[SessionMetaTenantID] = principal.TenantID
	}
	if principal.ActorID != "" {
		bound[SessionMetaActorID] = principal.ActorID
	}
	return bound, true
}
