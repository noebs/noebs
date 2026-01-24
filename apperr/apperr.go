package apperr

import (
	"errors"
	"net/http"
)

// Error represents a typed, status-aware application error.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message,omitempty"`
	Status  int            `json:"-"`
	Fields  map[string]any `json:"fields,omitempty"`
	Err     error          `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Code != "" {
		return e.Code
	}
	return "error"
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func New(code string, status int, message string) *Error {
	return &Error{Code: code, Status: status, Message: message}
}

func Wrap(err error, base *Error, message string) *Error {
	if err == nil {
		return nil
	}
	if base == nil {
		base = ErrInternal
	}
	copy := *base
	if message != "" {
		copy.Message = message
	}
	copy.Err = err
	return &copy
}

func WithFields(base *Error, fields map[string]any) *Error {
	if base == nil {
		return nil
	}
	copy := *base
	copy.Fields = fields
	return &copy
}

func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e, true
	}
	return nil, false
}

func Status(err error) int {
	if e, ok := As(err); ok && e.Status != 0 {
		return e.Status
	}
	return http.StatusInternalServerError
}

func Code(err error) string {
	if e, ok := As(err); ok && e.Code != "" {
		return e.Code
	}
	return "internal_error"
}

func Message(err error) string {
	if e, ok := As(err); ok {
		if e.Message != "" {
			return e.Message
		}
		if e.Err != nil {
			return e.Err.Error()
		}
		return e.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func Payload(err error) map[string]any {
	if err == nil {
		return map[string]any{}
	}
	if e, ok := As(err); ok {
		payload := map[string]any{
			"code":    Code(e),
			"message": Message(e),
		}
		if len(e.Fields) > 0 {
			payload["fields"] = e.Fields
		}
		return payload
	}
	return map[string]any{
		"code":    "internal_error",
		"message": err.Error(),
	}
}

var (
	ErrBadRequest   = New("bad_request", http.StatusBadRequest, "")
	ErrValidation   = New("validation_error", http.StatusBadRequest, "")
	ErrEmptyBody    = New("empty_body", http.StatusBadRequest, "request body is empty")
	ErrUnauthorized = New("unauthorized", http.StatusUnauthorized, "")
	ErrForbidden    = New("forbidden", http.StatusForbidden, "")
	ErrNotFound     = New("not_found", http.StatusNotFound, "")
	ErrConflict     = New("conflict", http.StatusConflict, "")
	ErrInternal     = New("internal_error", http.StatusInternalServerError, "")
	ErrUnavailable  = New("service_unavailable", http.StatusServiceUnavailable, "")
	ErrMarshal      = New("marshal_error", http.StatusInternalServerError, "")
	ErrDatabase     = New("database_error", http.StatusInternalServerError, "")
)
