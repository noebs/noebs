package dashboard

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
)

func jsonResponse(c *fiber.Ctx, code int, payload interface{}) {
	_ = c.Status(code).JSON(payload)
}

func parseJSON(c *fiber.Ctx, dst interface{}) error {
	if len(c.Body()) == 0 {
		return fiber.ErrBadRequest
	}
	return json.Unmarshal(c.Body(), dst)
}
