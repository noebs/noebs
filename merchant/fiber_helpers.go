package merchant

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

func jsonResponse(c *fiber.Ctx, code int, payload interface{}) {
	_ = c.Status(code).JSON(payload)
}
