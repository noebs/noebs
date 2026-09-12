package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) ResolveAccountDisplayName(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	principal, ok := gateway.InternalPrincipalIdentity(c)
	if !ok || principal.UserID <= 0 || !principal.HasRole(tenantauth.RoleUser) {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "authentication_failed", "message": "verified account principal is required"})
	}
	var request consumer.AccountDisplayNameRequest
	decoder := json.NewDecoder(bytes.NewReader(c.Body()))
	decoder.DisallowUnknownFields()
	if len(c.Body()) > 1024 || len(c.Context().QueryArgs().QueryString()) != 0 || decoder.Decode(&request) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF || request.UserID <= 0 {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "invalid_account_reference", "message": "one canonical account owner is required"})
	}
	result, err := h.Service.ResolveAccountDisplayName(c.UserContext(), principal.TenantID,
		consumer.PrincipalProjectionReference{Issuer: principal.Issuer, Subject: principal.Subject}, principal.UserID, request.UserID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return jsonResponse(c, http.StatusNotFound, fiber.Map{"code": "account_not_found", "message": "account display name is unavailable"})
	case errors.Is(err, consumer.ErrAccountDisplayNameActor):
		return jsonResponse(c, http.StatusForbidden, fiber.Map{"code": "authentication_failed", "message": "account principal does not match"})
	case err != nil:
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "account_name_unavailable", "message": "account display name is temporarily unavailable"})
	default:
		return jsonResponse(c, http.StatusOK, result)
	}
}
