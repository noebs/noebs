package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) OpaqueBalance(c *fiber.Ctx) error {
	tenantID, userID, ok := opaqueCardIdentity(c)
	if !ok {
		return nil
	}
	var req consumer.OpaqueBalanceRequest
	if err := decodeOpaqueBalanceRequest(c.Body(), &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": "invalid opaque balance request"})
	}
	result, err := h.Service.OpaqueBalance(c.UserContext(), tenantID, userID, req, time.Now().UTC())
	if err != nil {
		return fundedBalanceError(c, err)
	}
	return jsonResponse(c, http.StatusOK, result)
}

func (h *Handler) ClaimFundedCardOperationInternal(c *fiber.Ctx) error {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	var cmd consumer.ClaimFundedCardOperationCommand
	if err := bindJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	result, err := h.Service.ClaimFundedCardOperationForUserID(c.UserContext(), tenantID, cmd, time.Now().UTC())
	if err != nil {
		return opaqueCardError(c, err)
	}
	return jsonResponse(c, http.StatusOK, result)
}

func decodeOpaqueBalanceRequest(body []byte, dst *consumer.OpaqueBalanceRequest) error {
	if len(body) == 0 {
		return io.ErrUnexpectedEOF
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}

func fundedBalanceError(c *fiber.Ctx, err error) error {
	code := fundedBalanceErrorCode(err)
	status := statusForError(err)
	var callErr *ebs_fields.CallError
	if errors.As(err, &callErr) {
		code = consumer.ErrFundedRailRejected.Error()
		status = http.StatusBadGateway
	}
	return jsonResponse(c, status, fiber.Map{"code": code, "message": code})
}

func fundedBalanceErrorCode(err error) string {
	switch {
	case errors.Is(err, store.ErrCardNotFound):
		return store.ErrCardNotFound.Error()
	case errors.Is(err, store.ErrFundedClaimMismatch):
		return store.ErrFundedClaimMismatch.Error()
	case errors.Is(err, consumer.ErrOperationRailUUIDMismatch):
		return consumer.ErrOperationRailUUIDMismatch.Error()
	case errors.Is(err, consumer.ErrFundedOutcomeUnknown), errors.Is(err, consumer.ErrCardVaultCommand):
		return consumer.ErrFundedOutcomeUnknown.Error()
	case errors.Is(err, consumer.ErrFundedRailRejected):
		return consumer.ErrFundedRailRejected.Error()
	case errors.Is(err, consumer.ErrUnsafeBalanceResponse):
		return consumer.ErrUnsafeBalanceResponse.Error()
	case errors.Is(err, consumer.ErrFundedOperationsUnavailable),
		errors.Is(err, consumer.ErrMissingCardVault),
		errors.Is(err, consumer.ErrInvalidCardVault),
		errors.Is(err, consumer.ErrMissingEnrollmentPublicKey),
		errors.Is(err, consumer.ErrInvalidEnrollmentPublicKey):
		return consumer.ErrFundedOperationsUnavailable.Error()
	case statusForError(err) == http.StatusBadRequest:
		return "bad_request"
	default:
		return "internal_error"
	}
}
