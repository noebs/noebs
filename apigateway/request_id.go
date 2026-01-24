package gateway

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		requestID := strings.TrimSpace(c.Get(RequestIDHeader))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Locals("request_id", requestID)
		c.Set(RequestIDHeader, requestID)
		return c.Next()
	}
}

func RequestIDFromCtx(c *fiber.Ctx) string {
	if v := c.Locals("request_id"); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}
