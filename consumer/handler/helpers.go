package handler

import (
	"encoding/json"
	"errors"
	"net/http"

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
		errors.Is(err, store.ErrInvalidTenantID),
		errors.Is(err, store.ErrMissingUser),
		errors.Is(err, store.ErrMissingToken),
		errors.Is(err, store.ErrMissingUUID),
		errors.Is(err, store.ErrInvalidUserID),
		errors.Is(err, store.ErrMissingMobile),
		errors.Is(err, store.ErrInvalidMobile),
		errors.Is(err, store.ErrMissingEmail),
		errors.Is(err, store.ErrMissingUsername),
		errors.Is(err, store.ErrMissingUserIdentifier),
		errors.Is(err, store.ErrMissingPAN),
		errors.Is(err, store.ErrMissingData),
		errors.Is(err, store.ErrMissingBillType),
		errors.Is(err, store.ErrDuplicateAuthAccount),
		errors.Is(err, consumer.ErrMissingMobile),
		errors.Is(err, consumer.ErrMissingPassword),
		errors.Is(err, consumer.ErrMissingCardExpiry),
		errors.Is(err, consumer.ErrMissingUUID),
		errors.Is(err, consumer.ErrAmountMismatch),
		errors.Is(err, consumer.ErrCardNotMatched),
		errors.Is(err, consumer.ErrInvalidPaymentToken),
		errors.Is(err, consumer.ErrAmbiguousPaymentToken),
		errors.Is(err, consumer.ErrReceiverHasNoCard),
		errors.Is(err, consumer.ErrMissingBillerID),
		errors.Is(err, consumer.ErrMissingPublicKey),
		errors.Is(err, consumer.ErrInvalidCard),
		errors.Is(err, consumer.ErrUserAlreadyExists):
		return http.StatusBadRequest
	case errors.Is(err, consumer.ErrCardNotFound):
		return http.StatusNotFound
	case store.ErrNotFound(err):
		return http.StatusNotFound
	case errors.Is(err, consumer.ErrCardVaultCommand),
		errors.Is(err, consumer.ErrIdentityAuthCommand),
		errors.Is(err, consumer.ErrNotificationCommand),
		errors.Is(err, consumer.ErrBillerHookPost),
		errors.Is(err, consumer.ErrInvalidPaymentInfo),
		errors.Is(err, consumer.ErrMissingIssuedPAN),
		errors.Is(err, consumer.ErrInvalidRecoveryJWT):
		return http.StatusBadGateway
	case errors.Is(err, consumer.ErrMissingStore),
		errors.Is(err, consumer.ErrMissingService),
		errors.Is(err, consumer.ErrMissingAuth),
		errors.Is(err, consumer.ErrMissingHTTPClient),
		errors.Is(err, consumer.ErrMissingCardVault),
		errors.Is(err, consumer.ErrInvalidCardVault),
		errors.Is(err, consumer.ErrMissingIdentityAuth),
		errors.Is(err, consumer.ErrInvalidIdentityAuth),
		errors.Is(err, consumer.ErrMissingNotification),
		errors.Is(err, consumer.ErrInvalidNotification),
		errors.Is(err, consumer.ErrInvalidBillerHookEndpoint),
		errors.Is(err, store.ErrMissingDataKey),
		errors.Is(err, apperr.ErrUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
