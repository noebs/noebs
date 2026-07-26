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
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
	}
	cards, err := h.Service.ListOpaqueCardsForUserID(c.UserContext(), tenantID, userID)
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"cards": cards})
}

func (h *Handler) CreateOpaqueCardEnrollmentIntent(c *fiber.Ctx) error {
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
	}
	intent, err := h.Service.CreateOpaqueCardEnrollmentIntent(c.UserContext(), tenantID, userID)
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusCreated, intent)
}

func (h *Handler) ConfirmOpaqueCardEnrollment(c *fiber.Ctx) error {
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
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
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
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
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
	}
	if err := h.Service.RetireOpaqueCardForUserID(c.UserContext(), tenantID, userID, c.Params("card_id")); err != nil {
		return opaqueCardError(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) SetOpaqueMainCard(c *fiber.Ctx) error {
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
	}
	if err := h.Service.SetOpaqueMainCardForUserID(c.UserContext(), tenantID, userID, c.Params("card_id")); err != nil {
		return opaqueCardError(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) CreateCardEnrollmentIntentInternal(c *fiber.Ctx) error {
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
	}
	result, err := h.Service.CreateCardEnrollmentIntentForUserID(c.UserContext(), tenantID, userID, time.Now().UTC())
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusCreated, result)
}

func (h *Handler) BeginCardEnrollmentInternal(c *fiber.Ctx) error {
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
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
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
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
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
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
	tenantID, userID, err := opaqueCardIdentity(c)
	if err != nil {
		return opaqueCardIdentityError(c, err)
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

func opaqueCardIdentity(c *fiber.Ctx) (string, int64, error) {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return "", 0, err
	}
	userID, err := authenticatedUserID(c)
	if err != nil {
		return "", 0, err
	}
	return tenantID, userID, nil
}

func opaqueCardIdentityError(c *fiber.Ctx, err error) error {
	if errors.Is(err, store.ErrMissingTenantID) || errors.Is(err, store.ErrInvalidTenantID) {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "unauthorized", "message": "missing authenticated user identity"})
}

func opaqueCardError(c *fiber.Ctx, err error) error {
	status := statusForError(err)
	code := err.Error()
	if errors.Is(err, store.ErrCardNotFound) {
		code = store.ErrCardNotFound.Error()
	}
	return jsonResponse(c, status, fiber.Map{"code": code, "message": code})
}
