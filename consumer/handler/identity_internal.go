package handler

import (
	"database/sql"
	"errors"
	"net/http"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) ResolveProfileProjection(c *fiber.Ctx) error {
	principal, ok := gateway.InternalPrincipalIdentity(c)
	if !ok {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "authentication_failed", "message": "verified principal is required"})
	}
	profile, err := h.Service.ResolveProfileProjection(c.UserContext(), principal.TenantID, consumer.PrincipalProjectionReference{
		Issuer: principal.Issuer, Subject: principal.Subject,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse(c, http.StatusNotFound, fiber.Map{"code": "profile_not_found", "message": "profile projection not found"})
	}
	if err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, consumer.ResolveProfileProjectionResult{UserID: profile.UserID})
}
