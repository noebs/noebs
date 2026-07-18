package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) RequestPasswordRecovery(c *fiber.Ctx) error {
	var req consumer.PasswordRecoveryRequest
	if err := parseJSON(c, &req); err != nil || strings.TrimSpace(req.Mobile) == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": "A mobile number is required"})
	}
	tenantID, source, err := recoveryRequestContext(c)
	if err != nil {
		return recoveryContextError(c, err)
	}
	if err := h.Service.RequestPasswordRecovery(c.UserContext(), tenantID, req.Mobile, source, time.Now().UTC()); err != nil {
		return recoveryServiceError(c, err)
	}
	preventCredentialCaching(c)
	return jsonResponse(c, http.StatusAccepted, fiber.Map{
		"result":  "accepted",
		"message": "If the account is eligible, a recovery code will be sent.",
	})
}

func (h *Handler) VerifyPasswordRecovery(c *fiber.Ctx) error {
	var req consumer.PasswordRecoveryVerification
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": "Malformed request"})
	}
	tenantID, source, err := recoveryRequestContext(c)
	if err != nil {
		return recoveryContextError(c, err)
	}
	result, err := h.Service.VerifyPasswordRecoveryOTP(c.UserContext(), tenantID, req.Mobile, req.OTP, source, time.Now().UTC())
	if err != nil {
		if errors.Is(err, consumer.ErrInvalidRecoveryChallenge) {
			preventCredentialCaching(c)
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{
				"code":    consumer.ErrInvalidRecoveryChallenge.Error(),
				"message": "The recovery challenge is invalid or has expired",
			})
		}
		return recoveryServiceError(c, err)
	}
	preventCredentialCaching(c)
	return jsonResponse(c, http.StatusOK, result)
}

func (h *Handler) ResetPasswordWithRecovery(c *fiber.Ctx) error {
	var req consumer.PasswordRecoveryReset
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": "Malformed request"})
	}
	tenantID, source, err := recoveryRequestContext(c)
	if err != nil {
		return recoveryContextError(c, err)
	}
	if err := h.Service.ResetPasswordWithRecoveryCredential(c.UserContext(), tenantID, req, source, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, consumer.ErrInvalidRecoveryCredential):
			preventCredentialCaching(c)
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{
				"code":    consumer.ErrInvalidRecoveryCredential.Error(),
				"message": "The recovery credential is invalid or has expired",
			})
		case errors.Is(err, consumer.ErrPasswordInvalid):
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{
				"code":    consumer.ErrPasswordInvalid.Error(),
				"message": "Password must be at least 8 characters long and include a capital letter, a number, and a symbol",
			})
		case errors.Is(err, consumer.ErrMissingPublicKey), errors.Is(err, consumer.ErrInvalidPublicKey):
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{
				"code":    "invalid_public_key",
				"message": "A valid RSA public key is required",
			})
		default:
			return recoveryServiceError(c, err)
		}
	}
	preventCredentialCaching(c)
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) ValidateSession(c *fiber.Ctx) error {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	var cmd consumer.SessionValidationCommand
	if err := parseJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": "Malformed request"})
	}
	if err := h.Service.ValidateSession(c.UserContext(), tenantID, cmd); err != nil {
		if errors.Is(err, consumer.ErrSessionRevoked) {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "session_revoked", "message": "Session has been revoked"})
		}
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "session_validation_unavailable", "message": "Session validation is unavailable"})
	}
	return c.SendStatus(http.StatusNoContent)
}

func recoveryRequestContext(c *fiber.Ctx) (tenantID, source string, err error) {
	tenantID, err = resolveTenantID(c)
	if err != nil {
		return "", "", err
	}
	source, err = resolveRequestSource(c)
	if err != nil {
		return "", "", err
	}
	return tenantID, source, nil
}

func recoveryContextError(c *fiber.Ctx, err error) error {
	if errors.Is(err, consumer.ErrMissingRequestSource) || errors.Is(err, consumer.ErrInvalidRequestSource) {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "invalid_request_source", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
}

func recoveryServiceError(c *fiber.Ctx, err error) error {
	if errors.Is(err, consumer.ErrRateLimited) {
		return rateLimitResponse(c, err)
	}
	if errors.Is(err, consumer.ErrMissingStore) {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable", "message": "Recovery is temporarily unavailable"})
	}
	if errors.Is(err, store.ErrMissingTenantID) || errors.Is(err, store.ErrInvalidTenantID) {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"code": "service_error", "message": "Recovery is temporarily unavailable"})
}

func preventCredentialCaching(c *fiber.Ctx) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderPragma, "no-cache")
}
