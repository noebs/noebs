package consumer

import (
	"encoding/json"
	"net/http"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func bindJSON(c *fiber.Ctx, dst interface{}) error {
	if len(c.Body()) == 0 {
		return apperr.ErrEmptyBody
	}
	if err := json.Unmarshal(c.Body(), dst); err != nil {
		return apperr.Wrap(err, apperr.ErrBadRequest, err.Error())
	}
	if err := ebs_fields.ValidateStruct(dst); err != nil {
		return apperr.Wrap(err, apperr.ErrValidation, err.Error())
	}
	return nil
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

func getMobile(c *fiber.Ctx) string {
	if v := c.Locals("mobile"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getUserID(c *fiber.Ctx) int64 {
	if v := c.Locals("user_id"); v != nil {
		switch t := v.(type) {
		case uint:
			return int64(t)
		case int:
			return int64(t)
		case int64:
			return t
		case float64:
			return int64(t)
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

func getTenantID(c *fiber.Ctx) string {
	if v := c.Locals("tenant_id"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if v := c.Get("X-Tenant-ID"); v != "" {
		return v
	}
	return ""
}

func resolveTenantID(c *fiber.Ctx, cfg ebs_fields.NoebsConfig) string {
	if t := getTenantID(c); t != "" {
		return t
	}
	if cfg.DefaultTenantID != "" {
		return cfg.DefaultTenantID
	}
	return store.DefaultTenantID
}
