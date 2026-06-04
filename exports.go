package skawld

import (
	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/permissions"
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
type Tool = core.Tool
type ToolResult = core.ToolResult
type ToolContext = core.ToolContext
type SessionStore = core.SessionStore
type PermissionRule = permissions.Rule
type CanUseTool = permissions.CanUseTool

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
)
