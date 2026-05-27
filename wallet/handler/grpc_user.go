package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
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
	router.Post("/wallets", handler.EnsureWallet)
	router.Get("/wallets/:id/transactions", handler.ListWalletTransactions)
	router.Get("/wallets/:id", handler.GetWallet)
}

func (h *GRPCUserHandler) EnsureWallet(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	var req ensureWalletRequest
	if err := bindJSON(c, &req); err != nil {
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
	if err := validateRequestedTenantID(req.TenantID, tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedUserID(req.UserID, userID); err != nil {
		return jsonResponse(c, 0, err)
	}
	currency, err := resolveCurrency(h.Config, req.Currency)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	w, err := h.Client.EnsureWalletPublic(walletOutgoingContext(c), &walletv1.EnsureWalletRequest{
		TenantId: tenantID,
		UserId:   userID,
		Currency: currency,
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	resp, err := walletResponseFromProto(w)
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

	if _, err := authenticatedUserID(c); err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedTenantID(requestedTenantIDFromQuery(c), tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}

	w, err := h.Client.GetWalletPublic(walletOutgoingContext(c), &walletv1.GetWalletRequest{
		TenantId: tenantID,
		WalletId: walletID.String(),
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	resp, err := walletResponseFromProto(w)
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

	if _, err := authenticatedUserID(c); err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedTenantID(requestedTenantIDFromQuery(c), tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}
	limit, err := optionalIntQuery(c, "limit", 100)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidLimit))
	}
	offset, err := optionalIntQuery(c, "offset", 0)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidOffset))
	}

	entries, err := h.Client.ListWalletTransactionsPublic(walletOutgoingContext(c), &walletv1.ListWalletTransactionsRequest{
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
	if err := validateRequestedTenantID(requestedTenantIDFromQuery(c), tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}
	amount, err := optionalInt64Query(c, "amount")
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
	}
	limit, err := optionalIntQuery(c, "limit", 100)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidLimit))
	}
	offset, err := optionalIntQuery(c, "offset", 0)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidOffset))
	}

	methods, err := h.Client.ListPaymentMethodsPublic(walletOutgoingContext(c), &walletv1.ListPaymentMethodsRequest{
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

func walletOutgoingContext(c *fiber.Ctx) context.Context {
	return metadata.AppendToOutgoingContext(c.UserContext(), "authorization", c.Get(fiber.HeaderAuthorization))
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
		ID:               w.GetId(),
		TenantID:         w.GetTenantId(),
		OwnerType:        w.GetOwnerType(),
		OwnerID:          w.GetOwnerId(),
		UserID:           userID,
		Currency:         w.GetCurrency(),
		Balance:          w.GetBalance(),
		AvailableBalance: w.GetAvailableBalance(),
		Status:           w.GetStatus(),
		KYCTier:          w.GetKycTier(),
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
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
	return paymentMethodResponse{
		ProviderCode:     method.GetProviderCode(),
		ProviderName:     method.GetProviderName(),
		DisplayName:      method.GetDisplayName(),
		MethodType:       method.GetMethodType(),
		Direction:        method.GetDirection(),
		Currencies:       method.GetCurrencies(),
		Regions:          method.GetRegions(),
		MinAmount:        minAmount,
		MaxAmount:        maxAmount,
		InputSchema:      rawJSONFromString(method.GetInputSchemaJson()),
		Presentation:     rawJSONFromString(method.GetPresentationJson()),
		SupportsDeposit:  method.GetSupportsDeposit(),
		SupportsWithdraw: method.GetSupportsWithdrawal(),
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
		ID:             entry.GetId(),
		TenantID:       entry.GetTenantId(),
		TransactionID:  entry.GetTransactionId(),
		WalletID:       entry.GetWalletId(),
		EntryType:      entry.GetEntryType(),
		Amount:         entry.GetAmount(),
		Currency:       entry.GetCurrency(),
		BalanceAfter:   entry.GetBalanceAfter(),
		WalletSequence: entry.GetWalletSequence(),
		Status:         entry.GetStatus(),
		ReferenceType:  entry.GetReferenceType(),
		ReferenceID:    referenceID,
		Description:    description,
		Metadata:       rawJSONFromString(entry.GetMetadataJson()),
		CreatedAt:      createdAt,
	}, nil
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
	default:
		return apperr.Wrap(err, apperr.ErrInternal, st.Message())
	}
}
