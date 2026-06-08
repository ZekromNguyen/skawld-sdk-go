package core

import (
	"fmt"
	"time"
)

type ErrorKind string

const (
	ErrorAuth             ErrorKind = "auth"
	ErrorRateLimit        ErrorKind = "rate_limit"
	ErrorContextLength    ErrorKind = "context_length"
	ErrorPermissionDenied ErrorKind = "permission_denied"
	ErrorToolExecution    ErrorKind = "tool_execution"
	ErrorAbort            ErrorKind = "abort"
	ErrorProvider         ErrorKind = "provider"
	ErrorConfig           ErrorKind = "config"
	ErrorSkill            ErrorKind = "skill"
	ErrorSubagent         ErrorKind = "subagent"
)

type SkawldError struct {
	Kind       ErrorKind
	Message    string
	Retryable  bool
	Status     int
	ToolName   string
	Reason     string
	RetryAfter time.Duration
	Cause      error
}

func (e *SkawldError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *SkawldError) Unwrap() error { return e.Cause }

func (e *SkawldError) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	other, ok := target.(*SkawldError)
	if !ok {
		return false
	}
	if other.Kind != "" && e.Kind != other.Kind {
		return false
	}
	if other.ToolName != "" && e.ToolName != other.ToolName {
		return false
	}
	if other.Status != 0 && e.Status != other.Status {
		return false
	}
	return true
}

func NewConfigError(message string) *SkawldError {
	return &SkawldError{Kind: ErrorConfig, Message: message}
}

func NewAbortError(message string, cause error) *SkawldError {
	return &SkawldError{Kind: ErrorAbort, Message: message, Cause: cause}
}

func NewToolExecutionError(tool, message string) *SkawldError {
	return &SkawldError{Kind: ErrorToolExecution, Message: message, ToolName: tool}
}

func NewPermissionError(message string) *SkawldError {
	return &SkawldError{Kind: ErrorPermissionDenied, Message: message}
}

func NewProviderError(message string, status int, retryable bool, cause error) *SkawldError {
	return &SkawldError{Kind: ErrorProvider, Message: message, Status: status, Retryable: retryable, Cause: cause}
}
