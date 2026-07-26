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
