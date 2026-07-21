package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/transactionauth"
	walletrequest "github.com/adonese/noebs/wallet/request"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type GRPCUserHandler struct {
	Client walletv1.WalletPublicServiceClient
	Config ebs_fields.NoebsConfig
}

func NewGRPCUserHandler(client walletv1.WalletPublicServiceClient, cfg ebs_fields.NoebsConfig) *GRPCUserHandler {
	return &GRPCUserHandler{Client: client, Config: cfg}
}

func RegisterGRPCUserRoutes(router fiber.Router, handler *GRPCUserHandler) {
	router.Get("/methods", handler.ListPaymentMethods)
	router.Get("/currencies", handler.ListCurrencies)
	router.Get("/currencies/:code", handler.GetCurrency)
	router.Post("/money/parse", handler.ParseMoney)
	router.Post("/money/format", handler.FormatMoney)
	router.Post("/fx/quotes", handler.QuoteConversion)
	router.Get("/fx/quotes/:id", handler.GetConversionQuote)
	router.Get("/fx/sources", handler.ListFXSources)
	router.Post("/wallets", handler.EnsureWallet)
	router.Get("/wallets/:id/transactions", handler.ListWalletTransactions)
	router.Get("/wallets/:id", handler.GetWallet)
	router.Post("/deposits", handler.RequestDeposit)
	router.Post("/p2p", handler.RequestP2PTransfer)
	router.Post("/withdrawals", handler.RequestWithdrawal)
}

func (h *GRPCUserHandler) RequestDeposit(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	request, err := walletrequest.ParseDeposit(tenantID, c.Body())
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, err.Error()))
	}
	outgoing, err := walletOutgoingContext(c, tenantID, userID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	run, err := h.Client.RequestDeposit(outgoing, request)
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return jsonResponse(c, http.StatusAccepted, fiber.Map{
		"workflow_id": run.GetWorkflowId(),
		"run_id":      run.GetRunId(),
	})
}

func (h *GRPCUserHandler) RequestP2PTransfer(c *fiber.Ctx) error {
	return h.requestTransaction(c, transactionauth.OperationWalletP2P)
}

func (h *GRPCUserHandler) RequestWithdrawal(c *fiber.Ctx) error {
	return h.requestTransaction(c, transactionauth.OperationWalletWithdrawal)
}

func (h *GRPCUserHandler) requestTransaction(c *fiber.Ctx, operation transactionauth.Operation) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	canonical, err := walletrequest.ParseCanonical(operation, tenantID, c.Body())
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, err.Error()))
	}
	outgoing, err := walletOutgoingContext(c, tenantID, userID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	var run interface {
		GetWorkflowId() string
		GetRunId() string
	}
	switch request := canonical.Message.(type) {
	case *walletv1.RequestP2PTransferRequest:
		run, err = h.Client.RequestP2PTransfer(outgoing, request)
	case *walletv1.RequestWithdrawalRequest:
		run, err = h.Client.RequestWithdrawal(outgoing, request)
	default:
		return jsonResponse(c, 0, apperr.ErrInternal)
	}
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return jsonResponse(c, http.StatusAccepted, fiber.Map{
		"workflow_id": run.GetWorkflowId(),
		"run_id":      run.GetRunId(),
	})
}

func (h *GRPCUserHandler) EnsureWallet(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	var req ensureWalletRequest
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := rejectJSONField(c, "tenant_id"); err != nil {
		return jsonResponse(c, 0, err)
	}

	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedUserID(req.UserID, userID); err != nil {
		return jsonResponse(c, 0, err)
	}
	currency, err := resolveCurrency(req.Currency)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	outgoing, err := walletOutgoingContext(c, tenantID, userID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	w, err := h.Client.EnsureWalletPublic(outgoing, &walletv1.EnsureWalletPublicRequest{
		TenantId: tenantID,
		UserId:   userID,
		Currency: currency,
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	resp, err := walletResponseFromProto(w.GetWallet())
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	return jsonResponse(c, http.StatusOK, resp)
}

func (h *GRPCUserHandler) GetWallet(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	walletIDRaw := c.Params("id")
	if walletIDRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingWalletID))
	}
	walletID, err := uuid.Parse(walletIDRaw)
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid wallet id"))
	}

	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := rejectTenantIDQuery(c); err != nil {
		return jsonResponse(c, 0, err)
	}

	outgoing, err := walletOutgoingContext(c, tenantID, userID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	w, err := h.Client.GetWalletPublic(outgoing, &walletv1.GetWalletPublicRequest{
		TenantId: tenantID,
		WalletId: walletID.String(),
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	resp, err := walletResponseFromProto(w.GetWallet())
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	return jsonResponse(c, http.StatusOK, resp)
}

func (h *GRPCUserHandler) ListWalletTransactions(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	walletIDRaw := c.Params("id")
	if walletIDRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingWalletID))
	}
	walletID, err := uuid.Parse(walletIDRaw)
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid wallet id"))
	}

	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := rejectTenantIDQuery(c); err != nil {
		return jsonResponse(c, 0, err)
	}
	limit, err := positiveIntQuery(c, "limit", 100)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidLimit))
	}
	offset, err := nonNegativeIntQuery(c, "offset", 0)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidOffset))
	}

	outgoing, err := walletOutgoingContext(c, tenantID, userID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	entries, err := h.Client.ListWalletTransactionsPublic(outgoing, &walletv1.ListWalletTransactionsPublicRequest{
		TenantId:  tenantID,
		WalletId:  walletID.String(),
		EntryType: c.Query("entry_type"),
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	resp := make([]walletTransactionResponse, 0, len(entries.GetTransactions()))
	for _, entry := range entries.GetTransactions() {
		mapped, err := walletTransactionResponseFromProto(entry)
		if err != nil {
			return jsonResponse(c, 0, err)
		}
		resp = append(resp, mapped)
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"transactions": resp})
}

func (h *GRPCUserHandler) ListPaymentMethods(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := rejectTenantIDQuery(c); err != nil {
		return jsonResponse(c, 0, err)
	}
	amount, err := optionalNonNegativeInt64Query(c, "amount")
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
	}
	limit, err := positiveIntQuery(c, "limit", 100)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidLimit))
	}
	offset, err := nonNegativeIntQuery(c, "offset", 0)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidOffset))
	}

	outgoing, err := walletOutgoingContext(c, tenantID, userID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	methods, err := h.Client.ListPaymentMethodsPublic(outgoing, &walletv1.ListPaymentMethodsPublicRequest{
		TenantId:  tenantID,
		Direction: c.Query("direction"),
		Currency:  c.Query("currency"),
		Region:    c.Query("region"),
		Amount:    amount,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	resp := make([]paymentMethodResponse, 0, len(methods.GetMethods()))
	for _, method := range methods.GetMethods() {
		resp = append(resp, paymentMethodResponseFromProto(method))
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"methods": resp})
}

func walletOutgoingContext(c *fiber.Ctx, tenantID string, userID int64) (context.Context, error) {
	principal, ok := gateway.InternalPrincipalIdentity(c)
	if !ok || principal.TenantID != tenantID || principal.UserID != userID {
		return nil, apperr.ErrUnauthorized
	}
	return principalOutgoingContext(c.UserContext(), principal), nil
}

func principalOutgoingContext(ctx context.Context, principal gateway.PrincipalIdentity) context.Context {
	values := principal.HeaderValues()
	return metadata.NewOutgoingContext(ctx, metadata.Pairs(
		strings.ToLower(gateway.GatewayTenantIDHeader), values.TenantID,
		strings.ToLower(gateway.GatewayIssuerHeader), values.Issuer,
		strings.ToLower(gateway.GatewaySubjectHeader), values.Subject,
		strings.ToLower(gateway.GatewayOrganizationIDHeader), values.OrganizationID,
		strings.ToLower(gateway.GatewayAuthorizedPartyHeader), values.AuthorizedParty,
		strings.ToLower(gateway.GatewayRolesHeader), values.Roles,
		strings.ToLower(gateway.GatewayPermissionHeader), values.Permission,
		strings.ToLower(gateway.GatewayUserIDHeader), values.UserID,
		strings.ToLower(gateway.GatewaySourceIPHeader), values.SourceIP,
		strings.ToLower(gateway.GatewayTokenExpiresAtHeader), values.TokenExpiresAt,
	))
}

func walletResponseFromProto(w *walletv1.Wallet) (walletResponse, error) {
	if w == nil {
		return walletResponse{}, apperr.Wrap(errors.New("missing wallet response"), apperr.ErrInternal, "missing wallet response")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, w.GetCreatedAt())
	if err != nil {
		return walletResponse{}, apperr.Wrap(err, apperr.ErrInternal, "invalid wallet timestamp")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, w.GetUpdatedAt())
	if err != nil {
		return walletResponse{}, apperr.Wrap(err, apperr.ErrInternal, "invalid wallet timestamp")
	}
	var userID *int64
	if w.GetOwnerType() == walletstore.OwnerTypeUser {
		parsed, err := strconv.ParseInt(w.GetOwnerId(), 10, 64)
		if err != nil {
			return walletResponse{}, apperr.Wrap(err, apperr.ErrInternal, "invalid wallet owner id")
		}
		userID = &parsed
	}
	return walletResponse{
		ID:                    w.GetId(),
		TenantID:              w.GetTenantId(),
		OwnerType:             w.GetOwnerType(),
		OwnerID:               w.GetOwnerId(),
		UserID:                userID,
		Currency:              w.GetCurrency(),
		Balance:               w.GetBalance(),
		AvailableBalance:      w.GetAvailableBalance(),
		BalanceMoney:          moneyAmountResponseFromProto(w.GetBalanceMoney()),
		AvailableBalanceMoney: moneyAmountResponseFromProto(w.GetAvailableBalanceMoney()),
		Status:                w.GetStatus(),
		KYCTier:               w.GetKycTier(),
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
	}, nil
}

func paymentMethodResponseFromProto(method *walletv1.PaymentMethod) paymentMethodResponse {
	var minAmount *int64
	if method.MinAmount != nil {
		value := method.GetMinAmount()
		minAmount = &value
	}
	var maxAmount *int64
	if method.MaxAmount != nil {
		value := method.GetMaxAmount()
		maxAmount = &value
	}
	var currencyUnitVersionID string
	if method.GetCurrencyUnitVersionId() > 0 {
		currencyUnitVersionID = strconv.FormatInt(method.GetCurrencyUnitVersionId(), 10)
	}
	return paymentMethodResponse{
		ProviderCode:          method.GetProviderCode(),
		ProviderName:          method.GetProviderName(),
		DisplayName:           method.GetDisplayName(),
		MethodType:            method.GetMethodType(),
		Direction:             method.GetDirection(),
		Currencies:            method.GetCurrencies(),
		Regions:               method.GetRegions(),
		MinAmount:             minAmount,
		MaxAmount:             maxAmount,
		MinAmountMoney:        moneyAmountResponseFromProto(method.GetMinAmountMoney()),
		MaxAmountMoney:        moneyAmountResponseFromProto(method.GetMaxAmountMoney()),
		CurrencyUnitVersionID: currencyUnitVersionID,
		InputSchema:           rawJSONFromString(method.GetInputSchemaJson()),
		Presentation:          rawJSONFromString(method.GetPresentationJson()),
		SupportsDeposit:       method.GetSupportsDeposit(),
		SupportsWithdraw:      method.GetSupportsWithdrawal(),
	}
}

func walletTransactionResponseFromProto(entry *walletv1.WalletLedgerEntry) (walletTransactionResponse, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, entry.GetCreatedAt())
	if err != nil {
		return walletTransactionResponse{}, apperr.Wrap(err, apperr.ErrInternal, "invalid wallet ledger entry timestamp")
	}
	var referenceID *string
	if entry.ReferenceId != nil {
		value := entry.GetReferenceId()
		referenceID = &value
	}
	var description *string
	if entry.Description != nil {
		value := entry.GetDescription()
		description = &value
	}
	return walletTransactionResponse{
		ID:                entry.GetId(),
		TenantID:          entry.GetTenantId(),
		TransactionID:     entry.GetTransactionId(),
		WalletID:          entry.GetWalletId(),
		EntryType:         entry.GetEntryType(),
		Amount:            entry.GetAmount(),
		Currency:          entry.GetCurrency(),
		BalanceAfter:      entry.GetBalanceAfter(),
		AmountMoney:       moneyAmountResponseFromProto(entry.GetAmountMoney()),
		BalanceAfterMoney: moneyAmountResponseFromProto(entry.GetBalanceAfterMoney()),
		WalletSequence:    entry.GetWalletSequence(),
		Status:            entry.GetStatus(),
		ReferenceType:     entry.GetReferenceType(),
		ReferenceID:       referenceID,
		Description:       description,
		Metadata:          rawJSONFromString(entry.GetMetadataJson()),
		CreatedAt:         createdAt,
	}, nil
}

func moneyAmountResponseFromProto(amount *walletv1.MoneyAmount) *moneyAmountResponse {
	if amount == nil {
		return nil
	}
	return &moneyAmountResponse{
		MinorUnits:            amount.GetMinorUnits(),
		CurrencyCode:          amount.GetCurrencyCode(),
		CurrencyUnitVersionID: strconv.FormatInt(amount.GetCurrencyUnitVersionId(), 10),
		MinorExponent:         amount.GetMinorExponent(),
		MajorUnits:            amount.GetMajorUnits(),
		Display:               amount.GetDisplay(),
		Canonical:             amount.GetCanonical(),
	}
}

func rawJSONFromString(raw string) json.RawMessage {
	if raw == "" {
		return nil
	}
	return json.RawMessage(raw)
}

func mapWalletGRPCError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return apperr.Wrap(err, apperr.ErrInternal, err.Error())
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return apperr.Wrap(err, apperr.ErrBadRequest, st.Message())
	case codes.Unauthenticated:
		return apperr.Wrap(err, apperr.ErrUnauthorized, st.Message())
	case codes.PermissionDenied:
		return apperr.Wrap(err, apperr.ErrForbidden, st.Message())
	case codes.NotFound:
		return apperr.Wrap(err, apperr.ErrNotFound, st.Message())
	case codes.Unavailable, codes.FailedPrecondition:
		return apperr.Wrap(err, apperr.ErrUnavailable, st.Message())
	case codes.AlreadyExists:
		return apperr.Wrap(err, apperr.ErrConflict, st.Message())
	case codes.ResourceExhausted:
		return apperr.Wrap(err, apperr.ErrRateLimited, st.Message())
	case codes.Internal:
		return apperr.Wrap(err, apperr.ErrInternal, "internal wallet error")
	default:
		return apperr.Wrap(err, apperr.ErrInternal, "internal wallet error")
	}
}
