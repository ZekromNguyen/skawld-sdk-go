package policy

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type ApprovalOperation string

const (
	ApprovalGrantOperation  ApprovalOperation = "grant"
	ApprovalRejectOperation ApprovalOperation = "reject"
	ApprovalCancelOperation ApprovalOperation = "cancel"
)

type ApprovalAuthorization struct {
	Operation ApprovalOperation
	Approval  Approval
	Principal core.Principal
	Reason    string
}

type ApprovalAuthorizer interface {
	AuthorizeApproval(context.Context, ApprovalAuthorization) error
}

type ApprovalRolePolicyOptions struct {
	RoleCapabilities        map[string][]string
	OperationCapabilities   map[ApprovalOperation][]string
	RequireDistinctApprover bool
}

// ApprovalRolePolicy authorizes human approval transitions from trusted
// context roles. It never reads roles or capabilities from workflow data.
type ApprovalRolePolicy struct {
	roleCapabilities        map[string]map[string]struct{}
	operationCapabilities   map[ApprovalOperation][]string
	requireDistinctApprover bool
}

func NewApprovalRolePolicy(
	options ApprovalRolePolicyOptions,
) (*ApprovalRolePolicy, error) {
	if options.OperationCapabilities == nil {
		options.OperationCapabilities = map[ApprovalOperation][]string{
			ApprovalGrantOperation:  {"approval.grant"},
			ApprovalRejectOperation: {"approval.reject"},
			ApprovalCancelOperation: {"approval.cancel"},
		}
	}
	policy := &ApprovalRolePolicy{
		roleCapabilities: make(
			map[string]map[string]struct{}, len(options.RoleCapabilities),
		),
		operationCapabilities: make(
			map[ApprovalOperation][]string,
			len(options.OperationCapabilities),
		),
		requireDistinctApprover: options.RequireDistinctApprover,
	}
	for rawRole, values := range options.RoleCapabilities {
		role := strings.TrimSpace(rawRole)
		if !validPolicyIdentifier(role) {
			return nil, core.NewConfigError(
				fmt.Sprintf("invalid approval authorization role %q", rawRole),
			)
		}
		capabilities := make(map[string]struct{}, len(values))
		for _, rawCapability := range values {
			capability := strings.TrimSpace(rawCapability)
			if !validPolicyIdentifier(capability) {
				return nil, core.NewConfigError(fmt.Sprintf(
					"approval role %q contains invalid capability %q",
					role, rawCapability,
				))
			}
			capabilities[capability] = struct{}{}
		}
		policy.roleCapabilities[role] = capabilities
	}
	for operation, values := range options.OperationCapabilities {
		switch operation {
		case ApprovalGrantOperation, ApprovalRejectOperation,
			ApprovalCancelOperation:
		default:
			return nil, core.NewConfigError(fmt.Sprintf(
				"invalid approval operation %q", operation,
			))
		}
		required := uniqueSorted(values)
		if len(required) == 0 {
			return nil, core.NewConfigError(fmt.Sprintf(
				"approval operation %q requires at least one capability",
				operation,
			))
		}
		for _, capability := range required {
			if !validPolicyIdentifier(capability) {
				return nil, core.NewConfigError(fmt.Sprintf(
					"approval operation %q contains invalid capability %q",
					operation, capability,
				))
			}
		}
		policy.operationCapabilities[operation] = required
	}
	return policy, nil
}

func (p *ApprovalRolePolicy) AuthorizeApproval(
	ctx context.Context,
	request ApprovalAuthorization,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	authenticated, ok := core.PrincipalFromContext(ctx)
	if !ok || !authenticated.Authenticated() ||
		!request.Principal.Authenticated() ||
		authenticated.TenantID != request.Principal.TenantID ||
		authenticated.ActorID != request.Principal.ActorID {
		return core.NewPermissionError(
			"approval authorization requires the authenticated actor identity",
		)
	}
	if request.Approval.TenantID != authenticated.TenantID {
		return core.NewPermissionError("approval belongs to another tenant")
	}
	if request.Operation == ApprovalGrantOperation &&
		p.requireDistinctApprover {
		if request.Approval.RequestedBy == "" {
			return core.NewPermissionError(
				"approval requester identity is required for separation of duties",
			)
		}
		if request.Approval.RequestedBy == authenticated.ActorID {
			return core.NewPermissionError(
				"approval requester cannot grant their own request",
			)
		}
	}
	required, exists := p.operationCapabilities[request.Operation]
	if !exists {
		return core.NewPermissionError("approval operation is not authorized")
	}
	granted := make(map[string]struct{})
	for _, role := range authenticated.Roles {
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
		return core.NewPermissionError(
			"actor lacks required approval capabilities: " +
				strings.Join(missing, ", "),
		)
	}
	return nil
}

// AuthorizedApprovalStore applies approval authorization before mutating a
// persistence store. Expiration remains a deterministic deadline transition.
type AuthorizedApprovalStore struct {
	store      ApprovalLifecycleStore
	authorizer ApprovalAuthorizer
}

func NewAuthorizedApprovalStore(
	store ApprovalLifecycleStore,
	authorizer ApprovalAuthorizer,
) (*AuthorizedApprovalStore, error) {
	if store == nil || authorizer == nil {
		return nil, core.NewConfigError(
			"authorized approval store requires storage and authorization",
		)
	}
	return &AuthorizedApprovalStore{
		store: store, authorizer: authorizer,
	}, nil
}

func (s *AuthorizedApprovalStore) Request(
	ctx context.Context,
	approval Approval,
) (Approval, error) {
	return s.store.Request(ctx, approval)
}

func (s *AuthorizedApprovalStore) Durable() bool {
	store, ok := s.store.(DurableApprovalStore)
	return ok && store.Durable()
}

func (s *AuthorizedApprovalStore) Protected() bool {
	store, ok := s.store.(ProtectedApprovalStore)
	return ok && store.Protected()
}

func (s *AuthorizedApprovalStore) Get(
	ctx context.Context,
	approvalID string,
) (Approval, bool, error) {
	return s.store.Get(ctx, approvalID)
}

func (s *AuthorizedApprovalStore) List(
	ctx context.Context,
	filter ApprovalFilter,
) ([]Approval, error) {
	return s.store.List(ctx, filter)
}

func (s *AuthorizedApprovalStore) Decide(
	ctx context.Context,
	approvalID string,
	status ApprovalStatus,
	principal core.Principal,
	reason string,
) (Approval, error) {
	operation := ApprovalRejectOperation
	if status == ApprovalGranted {
		operation = ApprovalGrantOperation
	} else if status != ApprovalRejected {
		return Approval{}, core.NewConfigError(
			"approval decision must be granted or rejected",
		)
	}
	approval, exists, err := s.store.Get(ctx, approvalID)
	if err != nil {
		return Approval{}, err
	}
	if !exists {
		return Approval{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "approval not found",
		}
	}
	if err := s.authorizer.AuthorizeApproval(
		ctx, ApprovalAuthorization{
			Operation: operation, Approval: approval,
			Principal: principal, Reason: reason,
		},
	); err != nil {
		return Approval{}, err
	}
	return s.store.Decide(ctx, approvalID, status, principal, reason)
}

func (s *AuthorizedApprovalStore) Expire(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (Approval, error) {
	return s.store.Expire(ctx, approvalID, principal, reason)
}

func (s *AuthorizedApprovalStore) Cancel(
	ctx context.Context,
	approvalID string,
	principal core.Principal,
	reason string,
) (Approval, error) {
	approval, exists, err := s.store.Get(ctx, approvalID)
	if err != nil {
		return Approval{}, err
	}
	if !exists {
		return Approval{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "approval not found",
		}
	}
	if err := s.authorizer.AuthorizeApproval(
		ctx, ApprovalAuthorization{
			Operation: ApprovalCancelOperation, Approval: approval,
			Principal: principal, Reason: reason,
		},
	); err != nil {
		return Approval{}, err
	}
	return s.store.Cancel(ctx, approvalID, principal, reason)
}

var _ ApprovalLifecycleStore = (*AuthorizedApprovalStore)(nil)
