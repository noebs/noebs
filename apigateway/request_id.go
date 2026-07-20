package gateway

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

const maxRequestIDBytes = 256

func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		values := c.Request().Header.PeekAll(RequestIDHeader)
		var requestID string
		switch len(values) {
		case 0:
			requestID = uuid.NewString()
			c.Request().Header.Set(RequestIDHeader, requestID)
		case 1:
			requestID = string(values[0])
			if !validRequestID(requestID) {
				return c.Status(http.StatusBadRequest).JSON(fiber.Map{
					"code":    "invalid_request_id",
					"message": "X-Request-ID must be one printable token of at most 256 bytes",
				})
			}
		default:
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{
				"code":    "invalid_request_id",
				"message": "X-Request-ID must occur exactly once",
			})
		}
		c.Locals("request_id", requestID)
		c.Set(RequestIDHeader, requestID)
		return c.Next()
	}
}

func validRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func RequestIDFromCtx(c *fiber.Ctx) string {
	if v := c.Locals("request_id"); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}
