package consumer

import (
	"encoding/json"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func bindJSON(c *fiber.Ctx, dst interface{}) error {
	if len(c.Body()) == 0 {
		return fiber.ErrBadRequest
	}
	if err := json.Unmarshal(c.Body(), dst); err != nil {
		return err
	}
	return ebs_fields.ValidateStruct(dst)
}

func parseJSON(c *fiber.Ctx, dst interface{}) error {
	if len(c.Body()) == 0 {
		return fiber.ErrBadRequest
	}
	return json.Unmarshal(c.Body(), dst)
}

func jsonResponse(c *fiber.Ctx, code int, payload interface{}) {
	_ = c.Status(code).JSON(payload)
}

func getMobile(c *fiber.Ctx) string {
	if v := c.Locals("mobile"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getUserID(c *fiber.Ctx) uint {
	if v := c.Locals("user_id"); v != nil {
		switch t := v.(type) {
		case uint:
			return t
		case int:
			return uint(t)
		case int64:
			return uint(t)
		case float64:
			return uint(t)
		}
	}
	return 0
}

func getUsername(c *fiber.Ctx) string {
	if v := c.Locals("username"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if mobile := getMobile(c); mobile != "" {
		return mobile
	}
	return "anon"
}
