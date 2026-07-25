package skawld

import (
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/permissions"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
	"github.com/ZekromNguyen/skawld-sdk-go/tools/mcp"
)

type ModelID = core.ModelID
type Message = core.Message
type ContentBlock = core.ContentBlock
type ImageSource = core.ImageSource
type Usage = core.Usage
type StopReason = core.StopReason
type Event = core.Event
type EventType = core.EventType
type PermissionMode = core.PermissionMode
type Provider = core.Provider
type ProviderRequest = core.ProviderRequest
type ProviderStream = core.ProviderStream
type ProviderStreamEvent = core.ProviderStreamEvent
type ProviderStreamResult = core.ProviderStreamResult
type StreamingProvider = core.StreamingProvider
type LegacyStreamingProvider = core.LegacyStreamingProvider
type ProviderFactory = core.ProviderFactory
type Observer = core.Observer
type Observation = core.Observation
type ObservationType = core.ObservationType
type Tool = core.Tool
type ToolResult = core.ToolResult
type ToolContext = core.ToolContext
type ToolDescriptor = core.ToolDescriptor
type DescribedTool = core.DescribedTool
type IdempotentTool = core.IdempotentTool
type RiskLevel = core.RiskLevel
type SideEffectKind = core.SideEffectKind
type IdempotencySupport = core.IdempotencySupport
type Principal = core.Principal
type ContentTrust = core.ContentTrust
type FilesystemPolicy = tools.FilesystemPolicy
type SessionStore = core.SessionStore
type PermissionRule = permissions.Rule
type CanUseTool = permissions.CanUseTool
type MCPServerConfig = mcp.ServerConfig
type MCPStdioServerConfig = mcp.StdioServerConfig
type MCPHTTPServerConfig = mcp.HTTPServerConfig

const (
	PermissionModeDefault     = core.PermissionModeDefault
	PermissionModeAcceptEdits = core.PermissionModeAcceptEdits
	PermissionModeYolo        = core.PermissionModeYolo

	BlockText       = core.BlockText
	BlockToolUse    = core.BlockToolUse
	BlockToolResult = core.BlockToolResult
	BlockThinking   = core.BlockThinking
	BlockImage      = core.BlockImage

	RiskLow      = core.RiskLow
	RiskMedium   = core.RiskMedium
	RiskHigh     = core.RiskHigh
	RiskCritical = core.RiskCritical

	SideEffectNone          = core.SideEffectNone
	SideEffectIdempotent    = core.SideEffectIdempotent
	SideEffectNonIdempotent = core.SideEffectNonIdempotent
	SideEffectUnknown       = core.SideEffectUnknown

	IdempotencyNotApplicable = core.IdempotencyNotApplicable
	IdempotencyUnsupported   = core.IdempotencyUnsupported
	IdempotencyOptional      = core.IdempotencyOptional
	IdempotencyRequired      = core.IdempotencyRequired

	TrustSystemPolicy     = core.TrustSystemPolicy
	TrustHumanInstruction = core.TrustHumanInstruction
	TrustToolResult       = core.TrustToolResult
	TrustUntrustedContent = core.TrustUntrustedContent

	EventSystem            = core.EventSystem
	EventAssistant         = core.EventAssistant
	EventUser              = core.EventUser
	EventPartialAssistant  = core.EventPartialAssistant
	EventToolCallStart     = core.EventToolCallStart
	EventToolCallEnd       = core.EventToolCallEnd
	EventPermissionRequest = core.EventPermissionRequest
	EventUsage             = core.EventUsage
	EventCompaction        = core.EventCompaction
	EventResult            = core.EventResult
	EventError             = core.EventError
	EventSkillsLoaded      = core.EventSkillsLoaded
	EventSkillInvoked      = core.EventSkillInvoked
	EventSkillCompleted    = core.EventSkillCompleted
	EventSubagent          = core.EventSubagent

	ObservationProviderAttempt    = core.ObservationProviderAttempt
	ObservationToolExecution      = core.ObservationToolExecution
	ObservationPermissionCallback = core.ObservationPermissionCallback
	ObservationCompaction         = core.ObservationCompaction
	ObservationMCPCall            = core.ObservationMCPCall
	ObservationStoreOperation     = core.ObservationStoreOperation
)
