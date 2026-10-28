package lsp

import (
	"errors"
	"fmt"

	"github.com/totoer/goya/internal/tsfrontend/editorregex"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
)

// ErrorCode identifies errors from direct editor calls independently of JSON-RPC.
type ErrorCode string

const (
	CodeClosed            ErrorCode = "closed"
	CodeInvalidProject    ErrorCode = "invalid_project"
	CodeInvalidDocument   ErrorCode = "invalid_document"
	CodeContentModified   ErrorCode = "content_modified"
	CodeInvalidOptions    ErrorCode = "invalid_options"
	CodeInvalidHandle     ErrorCode = "invalid_handle"
	CodeInvalidEdit       ErrorCode = "invalid_edit"
	CodeResultExpired     ErrorCode = "result_expired"
	CodeReadOnly          ErrorCode = "read_only"
	CodeUnsupportedClient ErrorCode = "unsupported_client"
	CodeRequestLimit      ErrorCode = "request_limit"
)

// Error supports errors.As and errors.Is by Code; Cause retains host failures.
type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("lsp %s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("lsp %s: %s", e.Code, e.Message)
}
func (e *Error) Unwrap() error { return e.Cause }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

var (
	ErrClosed          = &Error{Code: CodeClosed, Message: "session is closed"}
	ErrContentModified = &Error{Code: CodeContentModified, Message: "editor result belongs to an outdated snapshot"}
	ErrResultExpired   = &Error{Code: CodeResultExpired, Message: "editor result was evicted from the bounded cache"}
	ErrReadOnly        = &Error{Code: CodeReadOnly, Message: "frozen declaration cannot be edited"}
	ErrRequestLimit    = &Error{Code: CodeRequestLimit, Message: "editor request exhausted its work budget"}
)

func publicServiceError(err error) error {
	var invalid *change.InvalidEdits
	if errors.As(err, &invalid) {
		return &Error{Code: CodeInvalidEdit, Message: invalid.Error(), Cause: invalid}
	}
	var budget *editorregex.BudgetExceeded
	if errors.As(err, &budget) {
		return &Error{Code: CodeRequestLimit, Message: budget.Error()}
	}
	return err
}
