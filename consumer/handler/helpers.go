package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/consumer"
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

func jsonResponse(c *fiber.Ctx, code int, payload interface{}) error {
	if err, ok := payload.(error); ok {
		status := code
		if status == 0 {
			status = apperr.Status(err)
		}
		return c.Status(status).JSON(apperr.Payload(err))
	}
	if code == 0 {
		code = http.StatusOK
	}
	return c.Status(code).JSON(payload)
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

func getTenantID(c *fiber.Ctx) string {
	if v := c.Locals("tenant_id"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if v := strings.TrimSpace(c.Get("X-Tenant-ID")); v != "" {
		return v
	}
	return ""
}

func resolveTenantID(c *fiber.Ctx, cfg ebs_fields.NoebsConfig) (string, error) {
	tenantID := getTenantID(c)
	if tenantID == "" {
		tenantID = strings.TrimSpace(cfg.DefaultTenantID)
	}
	if tenantID == "" {
		return "", store.ErrMissingTenantID
	}
	return tenantID, nil
}

func statusForError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var callErr *ebs_fields.CallError
	if errors.As(err, &callErr) && callErr != nil && callErr.Status != 0 {
		return callErr.Status
	}
	switch {
	case errors.Is(err, store.ErrMissingTenantID),
		errors.Is(err, store.ErrMissingUser),
		errors.Is(err, store.ErrMissingToken),
		errors.Is(err, store.ErrMissingUUID),
		errors.Is(err, store.ErrInvalidUserID),
		errors.Is(err, consumer.ErrMissingMobile),
		errors.Is(err, consumer.ErrMissingPassword),
		errors.Is(err, consumer.ErrMissingUUID),
		errors.Is(err, consumer.ErrAmountMismatch),
		errors.Is(err, consumer.ErrReceiverHasNoCard),
		errors.Is(err, consumer.ErrMissingPublicKey):
		return http.StatusBadRequest
	case store.ErrNotFound(err):
		return http.StatusNotFound
	case errors.Is(err, consumer.ErrMissingStore),
		errors.Is(err, consumer.ErrMissingService),
		errors.Is(err, store.ErrMissingDataKey),
		errors.Is(err, apperr.ErrUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
