package gateway

import (
	"errors"
	"net/http"

	"github.com/adonese/noebs/apperr"
	"github.com/gofiber/fiber/v2"
)

func JSONErrorHandler(c *fiber.Ctx, err error) error {
	status := http.StatusInternalServerError
	payload := map[string]any{
		"code":    "internal_error",
		"message": "internal server error",
	}

	if appErr, ok := apperr.As(err); ok {
		status = apperr.Status(appErr)
		payload = apperr.Payload(appErr)
	} else {
		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			status = fiberErr.Code
			if status < http.StatusInternalServerError {
				payload = map[string]any{
					"code":    "request_error",
					"message": fiberErr.Message,
				}
			} else if status == http.StatusBadGateway {
				payload = apperr.Payload(apperr.ErrBadGateway)
			} else if status == http.StatusServiceUnavailable {
				payload = apperr.Payload(apperr.ErrUnavailable)
			}
		}
	}
	if requestID := RequestIDFromCtx(c); requestID != "" {
		payload["request_id"] = requestID
	}
	return c.Status(status).JSON(payload)
}

type returnedHTTPError struct {
	err error
}

func (e *returnedHTTPError) Error() string {
	if appErr, ok := apperr.As(e.err); ok {
		return apperr.Code(appErr)
	}
	var fiberErr *fiber.Error
	if errors.As(e.err, &fiberErr) {
		switch fiberErr.Code {
		case http.StatusBadGateway:
			return "upstream_unavailable"
		case http.StatusServiceUnavailable:
			return "service_unavailable"
		default:
			return "request_error"
		}
	}
	return "internal_error"
}

func (e *returnedHTTPError) Unwrap() error {
	return e.err
}

func RedactReturnedErrors(c *fiber.Ctx) error {
	err := c.Next()
	if err == nil {
		return nil
	}
	return &returnedHTTPError{err: err}
}
