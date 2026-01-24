package dashboard

import (
	"encoding/json"
	"net/http"

	"github.com/adonese/noebs/apperr"
	"github.com/gofiber/fiber/v2"
)

func jsonResponse(c *fiber.Ctx, code int, payload interface{}) {
	if err, ok := payload.(error); ok {
		status := code
		if status == 0 {
			status = apperr.Status(err)
		}
		_ = c.Status(status).JSON(apperr.Payload(err))
		return
	}
	if code == 0 {
		code = http.StatusOK
	}
	_ = c.Status(code).JSON(payload)
}

func parseJSON(c *fiber.Ctx, dst interface{}) error {
	if len(c.Body()) == 0 {
		return apperr.ErrEmptyBody
	}
	if err := json.Unmarshal(c.Body(), dst); err != nil {
		return apperr.Wrap(err, apperr.ErrBadRequest, err.Error())
	}
	return nil
}
