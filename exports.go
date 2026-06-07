package skawld

import (
	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/permissions"
	"github.com/skawld/skawld-sdk-go/tools"
	"github.com/skawld/skawld-sdk-go/tools/mcp"
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
type Tool = core.Tool
type ToolResult = core.ToolResult
type ToolContext = core.ToolContext
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
)
