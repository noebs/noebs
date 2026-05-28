package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
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

func getTenantID(c *fiber.Ctx) string {
	if c == nil {
		return ""
	}
	if v := c.Locals("tenant_id"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func resolveTenantID(c *fiber.Ctx) (string, error) {
	return store.ValidateTenantID(getTenantID(c))
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
		errors.Is(err, store.ErrInvalidTenantID):
		return http.StatusBadRequest
	case store.ErrNotFound(err):
		return http.StatusNotFound
	case errors.Is(err, merchant.ErrMissingService),
		errors.Is(err, merchant.ErrMissingStore),
		errors.Is(err, merchant.ErrMissingHTTPClient),
		errors.Is(err, apperr.ErrUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
