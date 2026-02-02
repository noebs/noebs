package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/a-h/templ"
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

func renderComponent(c *fiber.Ctx, status int, component templ.Component) {
	if c == nil {
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	if component == nil {
		_ = c.SendStatus(status)
		return
	}
	ctx := c.UserContext()
	if ctx == nil {
		ctx = context.Background()
	}
	var buf bytes.Buffer
	if err := component.Render(ctx, &buf); err != nil {
		jsonResponse(c, http.StatusInternalServerError, apperr.Wrap(err, apperr.ErrInternal, err.Error()))
		return
	}
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	_ = c.Status(status).Send(buf.Bytes())
}
