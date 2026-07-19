package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/adonese/noebs/consumer"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) ResolveProfileProjection(c *fiber.Ctx) error {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "invalid_tenant_id", "message": err.Error()})
	}
	var reference consumer.PrincipalProjectionReference
	if err := bindStrictJSON(c, &reference); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	profile, err := h.Service.ResolveProfileProjection(c.UserContext(), tenantID, reference)
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse(c, http.StatusNotFound, fiber.Map{"code": "profile_not_found", "message": "profile projection not found"})
	}
	if err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, consumer.ResolveProfileProjectionResult{UserID: profile.UserID})
}

func (h *Handler) ResolveIdentityUserByMobile(c *fiber.Ctx) error {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var cmd consumer.IdentityUserByMobileCommand
	if err := parseJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	result, err := h.Service.ResolveIdentityUserByMobile(c.UserContext(), tenantID, cmd)
	if err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, result)
}

func (h *Handler) ResolveIdentityUsersBatch(c *fiber.Ctx) error {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var cmd consumer.IdentityUsersBatchCommand
	if err := parseJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	result, err := h.Service.ResolveIdentityUsersBatch(c.UserContext(), tenantID, cmd)
	if err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, result)
}
