package core

import (
	"context"
	"time"
)

type FileReadTracker interface {
	MarkRead(absPath string)
	HasRead(absPath string) bool
}

// ToolContext is passed to Tool.Execute for one tool invocation. It is safe to
// retain only for the duration of Execute; tools should honor Context
// cancellation and use Filesystem when resolving local paths.
type ToolContext struct {
	Context               context.Context
	CWD                   string
	Filesystem            FilesystemResolver
	FileReadTracker       FileReadTracker
	Observer              Observer
	Principal             Principal
	SessionID             string
	RunID                 string
	SessionStore          SessionStore
	StrictSessionIdentity bool
	Emit                  func(Event)
	InvokeSkill           func(context.Context, SkillInvocation) (ToolResult, error)
	RunSubagent           func(context.Context, SubagentInvocation) (ToolResult, error)
}

// FilesystemResolveMode describes the kind of filesystem access a built-in
// tool is resolving.
type FilesystemResolveMode string

const (
	FilesystemResolveRead   FilesystemResolveMode = "read"
	FilesystemResolveWrite  FilesystemResolveMode = "write"
	FilesystemResolveSearch FilesystemResolveMode = "search"
)

// FilesystemResolver resolves and authorizes tool paths for built-in
// filesystem tools.
type FilesystemResolver interface {
	Resolve(cwd, raw string, mode FilesystemResolveMode) (string, error)
}

type SkillInvocation struct {
	Name      string
	Arguments string
}

type SubagentInvocation struct {
	Name string
	Task string
}

type ToolResult struct {
	Content interface{}
	Summary string
	IsError bool
}

// RiskLevel describes the impact of a tool invocation. It is intentionally
// small and policy-oriented; applications can map these levels to their own
// approval and authorization systems.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// SideEffectKind describes whether retrying a tool may repeat an external
// effect. Unknown and non-idempotent effects must never be retried
// automatically by the SDK.
type SideEffectKind string

const (
	SideEffectNone          SideEffectKind = "none"
	SideEffectIdempotent    SideEffectKind = "idempotent"
	SideEffectNonIdempotent SideEffectKind = "non_idempotent"
	SideEffectUnknown       SideEffectKind = "unknown"
)

// IdempotencySupport describes how a tool handles caller-provided idempotency
// keys for external side effects.
type IdempotencySupport string

const (
	IdempotencyNotApplicable IdempotencySupport = "not_applicable"
	IdempotencyUnsupported   IdempotencySupport = "unsupported"
	IdempotencyOptional      IdempotencySupport = "optional"
	IdempotencyRequired      IdempotencySupport = "required"
)

// ToolDescriptor carries safety and execution metadata that is not sent to a
// model as prose. OutputSchema uses JSON Schema. Timeout is a runtime ceiling;
// zero means the application did not configure one.
type ToolDescriptor struct {
	Risk              RiskLevel
	SideEffect        SideEffectKind
	Idempotency       IdempotencySupport
	Timeout           time.Duration
	Permissions       []string
	OutputSchema      map[string]interface{}
	NetworkAccess     bool
	HandlesSecrets    bool
	ContainsUntrusted bool
}

// DescribedTool is an optional extension. Keeping it separate from Tool
// preserves compatibility with existing SDK tool implementations.
type DescribedTool interface {
	ToolDescriptor() ToolDescriptor
}

// IdempotentTool is an optional extension for tools that can bind external
// side effects to a caller-provided idempotency key.
type IdempotentTool interface {
	ExecuteIdempotent(input map[string]interface{}, idempotencyKey string, ctx ToolContext) (ToolResult, error)
}

// DescribeTool returns explicit metadata when supplied, otherwise conservative
// compatibility defaults derived from Scope.
func DescribeTool(tool Tool) ToolDescriptor {
	if described, ok := tool.(DescribedTool); ok {
		return described.ToolDescriptor()
	}
	if tool == nil {
		return ToolDescriptor{}
	}
	switch tool.Scope() {
	case ToolScopeRead:
		return ToolDescriptor{
			Risk:        RiskLow,
			SideEffect:  SideEffectNone,
			Idempotency: IdempotencyNotApplicable,
		}
	case ToolScopeWrite:
		return ToolDescriptor{
			Risk:        RiskMedium,
			SideEffect:  SideEffectUnknown,
			Idempotency: IdempotencyUnsupported,
		}
	default:
		return ToolDescriptor{
			Risk:        RiskHigh,
			SideEffect:  SideEffectUnknown,
			Idempotency: IdempotencyUnsupported,
		}
	}
}

// Tool is the extension contract for model-callable tools. Tool
// implementations registered in a shared Registry can be invoked concurrently
// when ParallelSafe returns true, so any mutable tool state must be protected or
// avoided.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]interface{}
	Scope() ToolScope
	ParallelSafe() bool
	Validate(raw map[string]interface{}) (map[string]interface{}, error)
	Execute(input map[string]interface{}, ctx ToolContext) (ToolResult, error)
	Summarize(input map[string]interface{}) string
}
