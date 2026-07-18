package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) ListOpaqueCards(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	cards, err := h.Service.ListOpaqueCardsForUserID(c.UserContext(), tenantID, userID)
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"cards": cards})
}

func (h *Handler) CreateOpaqueCardEnrollmentIntent(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	intent, err := h.Service.CreateOpaqueCardEnrollmentIntent(c.UserContext(), tenantID, userID)
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusCreated, intent)
}

func (h *Handler) ConfirmOpaqueCardEnrollment(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	var req consumer.ConfirmCardEnrollmentRequest
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	card, err := h.Service.ConfirmOpaqueCardEnrollment(c.UserContext(), tenantID, userID, c.Params("enrollment_id"), req)
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusCreated, card)
}

func (h *Handler) RenameOpaqueCard(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	var req struct {
		Name *string `json:"name"`
	}
	if err := bindJSON(c, &req); err != nil || req.Name == nil {
		if err == nil {
			err = store.ErrMissingData
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	if err := h.Service.RenameOpaqueCardForUserID(c.UserContext(), tenantID, userID, c.Params("card_id"), *req.Name); err != nil {
		return opaqueCardError(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) RetireOpaqueCard(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	if err := h.Service.RetireOpaqueCardForUserID(c.UserContext(), tenantID, userID, c.Params("card_id")); err != nil {
		return opaqueCardError(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) SetOpaqueMainCard(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	if err := h.Service.SetOpaqueMainCardForUserID(c.UserContext(), tenantID, userID, c.Params("card_id")); err != nil {
		return opaqueCardError(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) LegacyCardUpgradeRequired(c *fiber.Ctx) error {
	return jsonResponse(c, http.StatusGone, fiber.Map{
		"code":    consumer.ErrUpgradeRequired.Error(),
		"message": "This card endpoint has been retired; upgrade to opaque card references.",
	})
}

func (h *Handler) CreateCardEnrollmentIntentInternal(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	result, err := h.Service.CreateCardEnrollmentIntentForUserID(c.UserContext(), tenantID, userID, time.Now().UTC())
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusCreated, result)
}

func (h *Handler) BeginCardEnrollmentInternal(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	var cmd consumer.BeginCardEnrollmentCommand
	if err := bindJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	result, err := h.Service.BeginCardEnrollmentForUserID(c.UserContext(), tenantID, userID, cmd, time.Now().UTC())
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusOK, result)
}

func (h *Handler) ClaimCardEnrollmentRailInternal(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	var cmd consumer.ClaimCardEnrollmentRailCommand
	if err := bindJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	result, err := h.Service.ClaimCardEnrollmentRailForUserID(c.UserContext(), tenantID, userID, cmd, time.Now().UTC())
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusOK, result)
}

func (h *Handler) CompleteCardEnrollmentInternal(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	var cmd consumer.CompleteCardEnrollmentCommand
	if err := bindJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	card, err := h.Service.CompleteCardEnrollmentForUserID(c.UserContext(), tenantID, userID, cmd, time.Now().UTC())
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusOK, card)
}

func (h *Handler) FailCardEnrollmentInternal(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	var cmd consumer.FailCardEnrollmentCommand
	if err := bindJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	if err := h.Service.FailCardEnrollmentForUserID(c.UserContext(), tenantID, userID, cmd, time.Now().UTC()); err != nil {
		return opaqueCardError(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func opaqueCardIdentity(c *fiber.Ctx) (string, int64, bool) {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		_ = jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
		return "", 0, false
	}
	userID := getUserID(c)
	if userID <= 0 {
		_ = jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "unauthorized", "message": "missing authenticated user identity"})
		return "", 0, false
	}
	return tenantID, userID, true
}

func opaqueCardError(c *fiber.Ctx, err error) error {
	status := statusForError(err)
	code := err.Error()
	if errors.Is(err, store.ErrCardNotFound) {
		code = store.ErrCardNotFound.Error()
	}
	return jsonResponse(c, status, fiber.Map{"code": code, "message": code})
}
