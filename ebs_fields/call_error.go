package ebs_fields

import "fmt"

// CallError represents a failed EBS call.
//
// Status is the HTTP status code returned by the EBS transport layer.
// Response is the parsed EBS response body (best-effort).
// Err is the underlying cause (connectivity, gateway error, parsing, etc.).
type CallError struct {
	Status   int
	Response EBSParserFields
	Err      error
}

func (e *CallError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if msg := e.Response.ResponseMessage; msg != "" {
		return msg
	}
	if e.Status != 0 {
		return fmt.Sprintf("ebs call failed (status=%d)", e.Status)
	}
	return "ebs call failed"
}

func (e *CallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

