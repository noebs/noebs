package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/wallet"
	"github.com/adonese/noebs/wallet/rbac"
	walletstore "github.com/adonese/noebs/wallet/store"
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

func mapWalletError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, wallet.ErrMissingStore):
		return apperr.Wrap(err, apperr.ErrUnavailable, err.Error())
	case errors.Is(err, walletstore.ErrUserTwoFAAlreadyEnabled):
		return apperr.Wrap(err, apperr.ErrConflict, err.Error())
	case errors.Is(err, walletstore.ErrInvalidStatusTransition):
		return apperr.Wrap(err, apperr.ErrConflict, err.Error())
	case errors.Is(err, walletstore.ErrDuplicateWallet),
		errors.Is(err, walletstore.ErrDuplicateManualTransfer),
		errors.Is(err, walletstore.ErrDuplicateManualApproval),
		errors.Is(err, walletstore.ErrDuplicateVerification):
		return apperr.Wrap(err, apperr.ErrConflict, err.Error())
	case errors.Is(err, walletstore.ErrWalletNotFound),
		errors.Is(err, walletstore.ErrHoldNotFound),
		errors.Is(err, walletstore.ErrDestinationNotFound),
		errors.Is(err, walletstore.ErrVerificationNotFound),
		errors.Is(err, walletstore.ErrFundingSourceNotFound),
		errors.Is(err, walletstore.ErrPSPTransactionNotFound),
		errors.Is(err, walletstore.ErrUserTwoFANotFound):
		return apperr.Wrap(err, apperr.ErrNotFound, err.Error())
	case errors.Is(err, walletstore.ErrMissingTenantID),
		errors.Is(err, walletstore.ErrInvalidTenantID),
		errors.Is(err, walletstore.ErrMissingCurrency),
		errors.Is(err, walletstore.ErrMissingOwnerType),
		errors.Is(err, walletstore.ErrInvalidOwnerType),
		errors.Is(err, walletstore.ErrMissingOwnerID),
		errors.Is(err, walletstore.ErrMissingWalletID),
		errors.Is(err, walletstore.ErrInvalidUserID),
		errors.Is(err, walletstore.ErrMissingProviderCode),
		errors.Is(err, walletstore.ErrMissingDirection),
		errors.Is(err, walletstore.ErrInvalidDirection),
		errors.Is(err, walletstore.ErrMissingTransferType),
		errors.Is(err, walletstore.ErrMissingSourceType),
		errors.Is(err, walletstore.ErrMissingDestinationType),
		errors.Is(err, walletstore.ErrMissingDestinationDetails),
		errors.Is(err, walletstore.ErrMissingVerificationType),
		errors.Is(err, walletstore.ErrMissingVerificationTimeout),
		errors.Is(err, walletstore.ErrMissingVerificationTime),
		errors.Is(err, walletstore.ErrInvalidVerificationTime),
		errors.Is(err, walletstore.ErrMissingApprovalTimeout),
		errors.Is(err, walletstore.ErrMissingVerificationID),
		errors.Is(err, walletstore.ErrMissingIdempotencyKey),
		errors.Is(err, walletstore.ErrMissingReferenceType),
		errors.Is(err, walletstore.ErrMissingReferenceID),
		errors.Is(err, walletstore.ErrMissingHoldReason),
		errors.Is(err, walletstore.ErrMissingHoldExpiry),
		errors.Is(err, walletstore.ErrInvalidHoldID),
		errors.Is(err, walletstore.ErrInvalidAmount),
		errors.Is(err, walletstore.ErrInvalidPercentage),
		errors.Is(err, walletstore.ErrInvalidRate),
		errors.Is(err, walletstore.ErrInvalidWalletPair),
		errors.Is(err, walletstore.ErrMissingWalletPIN),
		errors.Is(err, walletstore.ErrInvalidWalletPIN),
		errors.Is(err, walletstore.ErrMissingTwoFACode),
		errors.Is(err, walletstore.ErrInvalidTwoFACode),
		errors.Is(err, walletstore.ErrMissingTwoFASecret),
		errors.Is(err, walletstore.ErrMissingApproverID),
		errors.Is(err, walletstore.ErrMissingDecision),
		errors.Is(err, walletstore.ErrInvalidDecision),
		errors.Is(err, walletstore.ErrMissingReason),
		errors.Is(err, walletstore.ErrMissingProofOfPayment),
		errors.Is(err, walletstore.ErrMissingStatus),
		errors.Is(err, walletstore.ErrInvalidStatus),
		errors.Is(err, walletstore.ErrMissingInteractionType),
		errors.Is(err, walletstore.ErrMissingSetBy),
		errors.Is(err, walletstore.ErrFundingSourceNotVerified),
		errors.Is(err, walletstore.ErrDestinationNotVerified),
		errors.Is(err, walletstore.ErrFundingSourceNotWithdrawable),
		errors.Is(err, walletstore.ErrFundingSourceLimitExceeded),
		errors.Is(err, walletstore.ErrApproverIsRequester),
		errors.Is(err, walletstore.ErrMissingStartTime),
		errors.Is(err, walletstore.ErrMissingEndTime),
		errors.Is(err, walletstore.ErrInvalidTimeRange),
		errors.Is(err, walletstore.ErrInvalidLimit),
		errors.Is(err, walletstore.ErrInvalidOffset),
		errors.Is(err, walletstore.ErrInsufficientFunds),
		errors.Is(err, walletstore.ErrCurrencyMismatch):
		return apperr.Wrap(err, apperr.ErrBadRequest, err.Error())
	default:
		return apperr.Wrap(err, apperr.ErrInternal, err.Error())
	}
}

func renderComponent(c *fiber.Ctx, status int, component templ.Component) error {
	if status == 0 {
		status = http.StatusOK
	}
	if component == nil {
		return c.SendStatus(status)
	}
	ctx := c.UserContext()
	if ctx == nil {
		ctx = context.Background()
	}
	var buf bytes.Buffer
	if err := component.Render(ctx, &buf); err != nil {
		return jsonResponse(c, http.StatusInternalServerError, apperr.Wrap(err, apperr.ErrInternal, err.Error()))
	}
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Status(status).Send(buf.Bytes())
}

func resolveTenantID(tenantID string) (string, error) {
	return walletstore.ValidateTenantID(tenantID)
}

func resolveCurrency(currency string) (string, error) {
	currency = strings.TrimSpace(currency)
	if currency == "" {
		return "", walletstore.ErrMissingCurrency
	}
	return currency, nil
}

func requirePermission(c *fiber.Ctx, perm rbac.Permission) error {
	if perm == "" {
		return nil
	}
	if c == nil {
		return apperr.ErrForbidden
	}
	if hasPermissionHeader(c, perm) {
		return nil
	}
	roleName := strings.TrimSpace(c.Get(gateway.GatewayAdminRoleHeader))
	if roleName != "" {
		if role := rbac.RoleForName(roleName); role != nil && role.HasPermission(perm) {
			return nil
		}
	}
	return apperr.ErrForbidden
}

func hasPermissionHeader(c *fiber.Ctx, perm rbac.Permission) bool {
	if c == nil {
		return false
	}
	header := strings.TrimSpace(c.Get(gateway.GatewayAdminPermissionsHeader))
	if header == "" {
		return false
	}
	for _, raw := range strings.Split(header, ",") {
		if strings.TrimSpace(raw) == string(perm) {
			return true
		}
	}
	return false
}
