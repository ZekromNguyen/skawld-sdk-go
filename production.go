package skawld

import (
	"fmt"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

// NewProductionAgent is an explicit production entry point. NewAgent accepts
// the same options for compatibility, but this constructor makes the intended
// safety profile visible at the call site.
func NewProductionAgent(opts AgentOptions) (*Agent, error) {
	if opts.Production == nil {
		return nil, core.NewConfigError(
			"production agent requires ProductionOptions",
		)
	}
	return NewAgent(opts)
}

func validateProductionAgentOptions(opts AgentOptions) error {
	if opts.Production == nil {
		return nil
	}
	production := opts.Production
	if !opts.Principal.Authenticated() {
		return core.NewConfigError(
			"production agent requires authenticated tenant and actor identities",
		)
	}
	if opts.Tools == nil {
		return core.NewConfigError(
			"production agent requires an explicit tool registry",
		)
	}
	if opts.SessionStore == nil {
		return core.NewConfigError(
			"production agent requires a durable session store",
		)
	}
	durableSessions, ok := opts.SessionStore.(core.DurableSessionStore)
	if !ok || !durableSessions.Durable() {
		return core.NewConfigError(
			"production agent requires a durable session store",
		)
	}
	protectedSessions, ok := opts.SessionStore.(core.ProtectedSessionStore)
	if !ok || !protectedSessions.Protected() {
		return core.NewConfigError(
			"production agent requires protected tenant-isolated session storage",
		)
	}
	if len(opts.MCPServers) > 0 {
		return core.NewConfigError(
			"production agent does not execute MCP tools directly; register them in the deterministic workflow runtime",
		)
	}
	if opts.Permissions.Mode == core.PermissionModeYolo {
		return core.NewConfigError(
			"production agent forbids yolo permission mode",
		)
	}
	if production.Policy == nil {
		return core.NewConfigError(
			"production agent requires a hard capability policy",
		)
	}
	capabilityPolicy, ok := production.Policy.(policy.CapabilityEvaluator)
	if !ok || !capabilityPolicy.EnforcesCapabilities() {
		return core.NewConfigError(
			"production agent requires a role/capability authorization policy",
		)
	}
	if production.AuditOutbox == nil {
		return core.NewConfigError(
			"production agent requires a durable audit outbox",
		)
	}
	durableOutbox, ok := production.AuditOutbox.(audit.DurableOutbox)
	if !ok || !durableOutbox.Durable() {
		return core.NewConfigError(
			"production agent requires a durable audit outbox",
		)
	}
	protectedOutbox, ok := production.AuditOutbox.(audit.ProtectedOutbox)
	if !ok || !protectedOutbox.Protected() {
		return core.NewConfigError(
			"production agent requires a protected audit outbox",
		)
	}
	if err := validateRuntimeLimits(production.Limits); err != nil {
		return err
	}
	for _, tool := range opts.Tools.List() {
		if err := validateProductionAgentTool(tool); err != nil {
			return err
		}
	}
	return nil
}

func validateProductionAgentTool(tool core.Tool) error {
	if tool == nil {
		return core.NewConfigError("production tool is nil")
	}
	name := tool.Name()
	descriptor := core.DescribeTool(tool)
	if descriptor.Timeout <= 0 {
		return core.NewConfigError(fmt.Sprintf(
			"production tool %q requires a finite timeout", name,
		))
	}
	if descriptor.SideEffect != core.SideEffectNone {
		return core.NewConfigError(fmt.Sprintf(
			"production agent tool %q has side effects; execute it through the deterministic workflow runtime",
			name,
		))
	}
	if len(descriptor.Permissions) == 0 {
		return core.NewConfigError(fmt.Sprintf(
			"production agent tool %q requires at least one capability",
			name,
		))
	}
	if len(tool.InputSchema()) == 0 {
		return core.NewConfigError(fmt.Sprintf(
			"production agent tool %q requires a trusted input schema",
			name,
		))
	}
	if len(descriptor.OutputSchema) == 0 {
		return core.NewConfigError(fmt.Sprintf(
			"production agent tool %q requires a trusted output schema",
			name,
		))
	}
	if err := workflow.ValidateToolSchemas(
		tool.InputSchema(), descriptor.OutputSchema, name,
	); err != nil {
		return core.NewConfigError(fmt.Sprintf(
			"production agent tool %q has an invalid contract: %v",
			name, err,
		))
	}
	return nil
}

func (a *Agent) validateProductionRuntimeTools() error {
	if a == nil || a.production() == nil {
		return nil
	}
	for _, tool := range a.opts.Tools.List() {
		if err := validateProductionAgentTool(tool); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeLimits(limits RuntimeLimits) error {
	switch {
	case limits.MaxRunDuration <= 0:
		return core.NewConfigError(
			"production agent requires a positive maximum run duration",
		)
	case limits.MaxToolCalls <= 0:
		return core.NewConfigError(
			"production agent requires a positive tool-call limit",
		)
	case limits.MaxToolResultBytes <= 0:
		return core.NewConfigError(
			"production agent requires a positive tool-result byte limit",
		)
	case limits.MaxProviderResponseBytes <= 0:
		return core.NewConfigError(
			"production agent requires a positive provider-response byte limit",
		)
	case limits.MaxProviderEvents <= 0:
		return core.NewConfigError(
			"production agent requires a positive provider-event limit",
		)
	case limits.MaxOutputTokensPerTurn <= 0:
		return core.NewConfigError(
			"production agent requires a positive per-turn output-token limit",
		)
	case limits.MaxSessionBytes <= 0:
		return core.NewConfigError(
			"production agent requires a positive session byte limit",
		)
	case limits.MaxTotalTokens <= 0:
		return core.NewConfigError(
			"production agent requires a positive total-token limit",
		)
	case limits.MaxOutputTokensPerTurn > limits.MaxTotalTokens:
		return core.NewConfigError(
			"production per-turn output-token limit cannot exceed the total-token limit",
		)
	}
	return nil
}

func (a *Agent) production() *ProductionOptions {
	if a == nil {
		return nil
	}
	return a.opts.Production
}
