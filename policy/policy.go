// Package policy is the mandatory safety boundary between proposed actions and
// real-world execution.
package policy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/internal/id"
)

type DecisionKind string

const (
	Allow           DecisionKind = "allow"
	Deny            DecisionKind = "deny"
	RequireApproval DecisionKind = "require_approval"
)

type Action struct {
	Principal       core.Principal
	ExecutionID     string
	WorkflowID      string
	WorkflowVersion int
	StepID          string
	ToolName        string
	Input           map[string]interface{}
	Descriptor      core.ToolDescriptor
	Reason          string
}

type Decision struct {
	Kind   DecisionKind
	Reason string
}

type Evaluator interface {
	Evaluate(context.Context, Action) (Decision, error)
}

// RolePolicyOptions maps trusted application roles to exact tool
// capabilities. Capability names come from ToolDescriptor.Permissions and are
// never taken from model output. Next defaults to RiskPolicy.
type RolePolicyOptions struct {
	RoleCapabilities map[string][]string
	Next             Evaluator
}

// RolePolicy enforces actor capabilities before delegating to risk and
// approval policy. A tool with no declared permissions is delegated directly;
// a tool declaring permissions is denied unless the actor's trusted roles
// collectively grant every required capability.
type RolePolicy struct {
	roleCapabilities map[string]map[string]struct{}
	next             Evaluator
}

func NewRolePolicy(options RolePolicyOptions) (*RolePolicy, error) {
	if options.Next == nil {
		options.Next = RiskPolicy{}
	}
	policy := &RolePolicy{
		roleCapabilities: make(map[string]map[string]struct{}, len(options.RoleCapabilities)),
		next:             options.Next,
	}
	for rawRole, rawCapabilities := range options.RoleCapabilities {
		role := strings.TrimSpace(rawRole)
		if !validPolicyIdentifier(role) {
			return nil, core.NewConfigError(
				fmt.Sprintf("invalid authorization role %q", rawRole),
			)
		}
		capabilities := make(map[string]struct{}, len(rawCapabilities))
		for _, rawCapability := range rawCapabilities {
			capability := strings.TrimSpace(rawCapability)
			if !validPolicyIdentifier(capability) {
				return nil, core.NewConfigError(
					fmt.Sprintf(
						"role %q contains invalid capability %q",
						role, rawCapability,
					),
				)
			}
			capabilities[capability] = struct{}{}
		}
		policy.roleCapabilities[role] = capabilities
	}
	return policy, nil
}

func (p *RolePolicy) Evaluate(
	ctx context.Context,
	action Action,
) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if !action.Principal.Authenticated() {
		return Decision{
			Kind: Deny, Reason: "authenticated tenant and actor are required",
		}, nil
	}
	required := uniqueSorted(action.Descriptor.Permissions)
	if len(required) > 0 {
		granted := make(map[string]struct{})
		for _, role := range action.Principal.Roles {
			for capability := range p.roleCapabilities[strings.TrimSpace(role)] {
				granted[capability] = struct{}{}
			}
		}
		missing := make([]string, 0)
		for _, capability := range required {
			if _, exists := granted[capability]; !exists {
				missing = append(missing, capability)
			}
		}
		if len(missing) > 0 {
			return Decision{
				Kind: Deny,
				Reason: "actor lacks required capabilities: " +
					strings.Join(missing, ", "),
			}, nil
		}
	}
	return p.next.Evaluate(ctx, action)
}

func validPolicyIdentifier(value string) bool {
	if value == "" || len(value) > 256 ||
		strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._:/-", character) {
			continue
		}
		return false
	}
	return true
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	output := make([]string, 0, len(set))
	for value := range set {
		output = append(output, value)
	}
	sort.Strings(output)
	return output
}

// RiskPolicy provides conservative defaults: high/critical risk, unknown or
// non-idempotent side effects, and network access require approval unless an
// application provides a more specific evaluator.
type RiskPolicy struct{}

func (RiskPolicy) Evaluate(ctx context.Context, action Action) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	switch {
	case action.Descriptor.Risk == core.RiskCritical:
		return Decision{Kind: RequireApproval, Reason: "critical-risk action"}, nil
	case action.Descriptor.SideEffect == core.SideEffectUnknown:
		return Decision{Kind: RequireApproval, Reason: "unknown side effect"}, nil
	case action.Descriptor.SideEffect == core.SideEffectNonIdempotent:
		return Decision{Kind: RequireApproval, Reason: "non-idempotent side effect"}, nil
	case action.Descriptor.Risk == core.RiskHigh:
		return Decision{Kind: RequireApproval, Reason: "high-risk action"}, nil
	case action.Descriptor.NetworkAccess:
		return Decision{Kind: RequireApproval, Reason: "network access"}, nil
	default:
		return Decision{Kind: Allow, Reason: "risk policy allowed action"}, nil
	}
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalGranted  ApprovalStatus = "granted"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
	ApprovalCanceled ApprovalStatus = "canceled"
)

type Approval struct {
	ID          string                 `json:"id"`
	TenantID    string                 `json:"tenant_id,omitempty"`
	ExecutionID string                 `json:"execution_id"`
	StepID      string                 `json:"step_id"`
	ToolName    string                 `json:"tool_name,omitempty"`
	Summary     string                 `json:"summary"`
	InputHash   string                 `json:"input_hash,omitempty"`
	Risk        core.RiskLevel         `json:"risk"`
	Status      ApprovalStatus         `json:"status"`
	RequestedBy string                 `json:"requested_by,omitempty"`
	RequestedAt time.Time              `json:"requested_at"`
	ExpiresAt   time.Time              `json:"expires_at,omitempty"`
	DecidedAt   time.Time              `json:"decided_at,omitempty"`
	DecidedBy   string                 `json:"decided_by,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type ApprovalStore interface {
	Request(context.Context, Approval) (Approval, error)
	Get(context.Context, string) (Approval, bool, error)
	Decide(context.Context, string, ApprovalStatus, core.Principal, string) (Approval, error)
}

type ApprovalFilter struct {
	ExecutionID string
	Status      ApprovalStatus
	Limit       int
}

// ApprovalLifecycleStore adds lifecycle operations without forcing minimal
// third-party ApprovalStore adapters to implement administrative queries.
type ApprovalLifecycleStore interface {
	ApprovalStore
	List(context.Context, ApprovalFilter) ([]Approval, error)
	Expire(context.Context, string, core.Principal, string) (Approval, error)
	Cancel(context.Context, string, core.Principal, string) (Approval, error)
}

type MemoryApprovalStore struct {
	mu    sync.RWMutex
	items map[string]Approval
	now   func() time.Time
}

func NewMemoryApprovalStore() *MemoryApprovalStore {
	return NewMemoryApprovalStoreWithClock(
		func() time.Time { return time.Now().UTC() },
	)
}

// NewMemoryApprovalStoreWithClock supports deterministic lifecycle tests and
// embeddings with an application-controlled clock.
func NewMemoryApprovalStoreWithClock(
	now func() time.Time,
) *MemoryApprovalStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryApprovalStore{
		items: make(map[string]Approval),
		now:   now,
	}
}

func (s *MemoryApprovalStore) Request(ctx context.Context, approval Approval) (Approval, error) {
	if err := ctx.Err(); err != nil {
		return Approval{}, err
	}
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || !principal.Authenticated() {
		return Approval{}, core.NewPermissionError(
			"approval request requires authenticated tenant and actor identities",
		)
	}
	if approval.TenantID != "" && approval.TenantID != principal.TenantID {
		return Approval{}, core.NewPermissionError("approval belongs to another tenant")
	}
	approval.TenantID = principal.TenantID
	if approval.ID == "" {
		approval.ID = id.New()
	}
	if approval.Status == "" {
		approval.Status = ApprovalPending
	}
	if approval.Status != ApprovalPending {
		return Approval{}, core.NewConfigError(
			"new approval status must be pending",
		)
	}
	if approval.RequestedBy != "" &&
		approval.RequestedBy != principal.ActorID {
		return Approval{}, core.NewPermissionError(
			"approval requester does not match authenticated actor",
		)
	}
	approval.RequestedBy = principal.ActorID
	if approval.RequestedAt.IsZero() {
		approval.RequestedAt = s.now()
	}
	if !approval.ExpiresAt.IsZero() &&
		!approval.ExpiresAt.After(approval.RequestedAt) {
		return Approval{}, core.NewConfigError(
			"approval expiration must be after its request time",
		)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[approval.ID] = approval
	return approval, nil
}

func (s *MemoryApprovalStore) Get(ctx context.Context, approvalID string) (Approval, bool, error) {
	if err := ctx.Err(); err != nil {
		return Approval{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	approval, ok := s.items[approvalID]
	principal, _ := core.PrincipalFromContext(ctx)
	if ok && approval.TenantID != "" && approval.TenantID != principal.TenantID {
		return Approval{}, false, core.NewPermissionError("approval belongs to another tenant")
	}
	return approval, ok, nil
}

func (s *MemoryApprovalStore) Decide(ctx context.Context, approvalID string, status ApprovalStatus, principal core.Principal, reason string) (Approval, error) {
	if err := ctx.Err(); err != nil {
		return Approval{}, err
	}
	if status != ApprovalGranted && status != ApprovalRejected {
		return Approval{}, core.NewConfigError("approval decision must be granted or rejected")
	}
	if err := authenticateApprovalActor(ctx, principal); err != nil {
		return Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(approvalID, status, principal, reason, false)
}

func (s *MemoryApprovalStore) Expire(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (Approval, error) {
	if err := authenticateApprovalActor(ctx, principal); err != nil {
		return Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(
		approvalID, ApprovalExpired, principal, reason, true,
	)
}

func (s *MemoryApprovalStore) Cancel(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (Approval, error) {
	if err := authenticateApprovalActor(ctx, principal); err != nil {
		return Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(
		approvalID, ApprovalCanceled, principal, reason, false,
	)
}

func (s *MemoryApprovalStore) transitionLocked(
	approvalID string,
	status ApprovalStatus,
	principal core.Principal,
	reason string,
	requireDue bool,
) (Approval, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > 4096 || strings.ContainsRune(reason, '\x00') {
		return Approval{}, core.NewConfigError(
			"approval reason exceeds its safe bounds",
		)
	}
	approval, ok := s.items[approvalID]
	if !ok {
		return Approval{}, &core.SkawldError{Kind: core.ErrorNotFound, Message: "approval not found"}
	}
	if approval.TenantID != "" && approval.TenantID != principal.TenantID {
		return Approval{}, core.NewPermissionError("approval belongs to another tenant")
	}
	if approval.Status != ApprovalPending {
		return Approval{}, &core.SkawldError{Kind: core.ErrorConflict, Message: "approval is already decided"}
	}
	if requireDue &&
		(approval.ExpiresAt.IsZero() || s.now().Before(approval.ExpiresAt)) {
		return Approval{}, &core.SkawldError{
			Kind: core.ErrorConflict, Message: "approval is not due to expire",
		}
	}
	approval.Status = status
	approval.DecidedAt = s.now()
	approval.DecidedBy = principal.ActorID
	approval.Reason = reason
	s.items[approvalID] = approval
	return approval, nil
}

func (s *MemoryApprovalStore) List(
	ctx context.Context,
	filter ApprovalFilter,
) ([]Approval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || !principal.Authenticated() {
		return nil, core.NewPermissionError(
			"approval listing requires authenticated tenant and actor identities",
		)
	}
	if err := validateApprovalFilter(filter); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Approval, 0)
	for _, approval := range s.items {
		if approval.TenantID != principal.TenantID ||
			filter.ExecutionID != "" &&
				approval.ExecutionID != filter.ExecutionID ||
			filter.Status != "" && approval.Status != filter.Status {
			continue
		}
		output = append(output, approval)
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].RequestedAt.Equal(output[j].RequestedAt) {
			return output[i].ID < output[j].ID
		}
		return output[i].RequestedAt.Before(output[j].RequestedAt)
	})
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	if len(output) > limit {
		output = output[:limit]
	}
	return output, nil
}

func authenticateApprovalActor(
	ctx context.Context,
	principal core.Principal,
) error {
	authenticated, ok := core.PrincipalFromContext(ctx)
	if !ok || !authenticated.Authenticated() ||
		!principal.Authenticated() ||
		authenticated.TenantID != principal.TenantID ||
		authenticated.ActorID != principal.ActorID {
		return core.NewPermissionError(
			"approval action requires the authenticated actor identity",
		)
	}
	return nil
}

func validateApprovalFilter(filter ApprovalFilter) error {
	if filter.Limit < 0 || filter.Limit > 1000 {
		return core.NewConfigError(
			"approval list limit must be between 0 and 1000",
		)
	}
	switch filter.Status {
	case "", ApprovalPending, ApprovalGranted, ApprovalRejected,
		ApprovalExpired, ApprovalCanceled:
		return nil
	default:
		return core.NewConfigError("approval list status is invalid")
	}
}
