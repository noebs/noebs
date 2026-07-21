package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/adonese/noebs/apperr"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var errClientControlledQuoteRounding = errors.New("rounding_mode is not accepted for FX quotes")

type parseMoneyRequest struct {
	CurrencyCode          string `json:"currency_code"`
	CurrencyUnitVersionID string `json:"currency_unit_version_id"`
	MajorUnits            string `json:"major_units"`
	RoundingMode          string `json:"rounding_mode"`
}

type formatMoneyRequest struct {
	CurrencyCode          string `json:"currency_code"`
	CurrencyUnitVersionID string `json:"currency_unit_version_id"`
	MinorUnits            string `json:"minor_units"`
	RoundingMode          string `json:"rounding_mode"`
}

type quoteConversionRequest struct {
	IdempotencyKey             string `json:"idempotency_key"`
	SourceCode                 string `json:"source_code"`
	BaseCurrency               string `json:"base_currency"`
	BaseCurrencyUnitVersionID  string `json:"base_currency_unit_version_id"`
	QuoteCurrency              string `json:"quote_currency"`
	QuoteCurrencyUnitVersionID string `json:"quote_currency_unit_version_id"`
	InputMinorUnits            string `json:"input_minor_units"`
	Side                       string `json:"side"`
}

func (h *GRPCUserHandler) ListCurrencies(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	outgoing, tenantID, _, err := h.moneyRequestContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.ListCurrenciesPublic(outgoing, &walletv1.ListCurrenciesPublicRequest{TenantId: tenantID})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, http.StatusOK, response)
}

func (h *GRPCUserHandler) GetCurrency(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	outgoing, tenantID, _, err := h.moneyRequestContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetCurrencyPublic(outgoing, &walletv1.GetCurrencyPublicRequest{
		TenantId:     tenantID,
		CurrencyCode: c.Params("code"),
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, http.StatusOK, response)
}

func (h *GRPCUserHandler) ParseMoney(c *fiber.Ctx) error {
	var request parseMoneyRequest
	if err := h.bindMoneyRequest(c, &request); err != nil {
		return jsonResponse(c, 0, err)
	}
	currencyUnitID, err := parseCanonicalCurrencyUnitID(request.CurrencyUnitVersionID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	outgoing, tenantID, _, err := h.moneyRequestContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.ParseMoneyPublic(outgoing, &walletv1.ParseMoneyPublicRequest{
		TenantId:              tenantID,
		CurrencyCode:          request.CurrencyCode,
		CurrencyUnitVersionId: currencyUnitID,
		MajorUnits:            request.MajorUnits,
		RoundingMode:          request.RoundingMode,
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, http.StatusOK, response)
}

func (h *GRPCUserHandler) FormatMoney(c *fiber.Ctx) error {
	var request formatMoneyRequest
	if err := h.bindMoneyRequest(c, &request); err != nil {
		return jsonResponse(c, 0, err)
	}
	currencyUnitID, err := parseCanonicalCurrencyUnitID(request.CurrencyUnitVersionID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	outgoing, tenantID, _, err := h.moneyRequestContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.FormatMoneyPublic(outgoing, &walletv1.FormatMoneyPublicRequest{
		TenantId:              tenantID,
		CurrencyCode:          request.CurrencyCode,
		CurrencyUnitVersionId: currencyUnitID,
		MinorUnits:            request.MinorUnits,
		RoundingMode:          request.RoundingMode,
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, http.StatusOK, response)
}

func (h *GRPCUserHandler) QuoteConversion(c *fiber.Ctx) error {
	var request quoteConversionRequest
	if err := h.bindMoneyRequest(c, &request); err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := rejectQuoteRoundingMode(c); err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := walletstore.ValidateIdempotencyKey(request.IdempotencyKey); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	baseCurrencyUnitID, err := parseCanonicalCurrencyUnitID(request.BaseCurrencyUnitVersionID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	quoteCurrencyUnitID, err := parseCanonicalCurrencyUnitID(request.QuoteCurrencyUnitVersionID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	outgoing, tenantID, userID, err := h.moneyRequestContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.QuoteConversionPublic(outgoing, &walletv1.QuoteConversionPublicRequest{
		TenantId:                   tenantID,
		UserId:                     userID,
		IdempotencyKey:             request.IdempotencyKey,
		SourceCode:                 request.SourceCode,
		BaseCurrency:               request.BaseCurrency,
		BaseCurrencyUnitVersionId:  baseCurrencyUnitID,
		QuoteCurrency:              request.QuoteCurrency,
		QuoteCurrencyUnitVersionId: quoteCurrencyUnitID,
		InputMinorUnits:            request.InputMinorUnits,
		Side:                       request.Side,
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, http.StatusCreated, response)
}

func parseCanonicalCurrencyUnitID(value string) (int64, error) {
	if value == "" {
		return 0, currencyUnitIDRequestError(walletstore.ErrMissingCurrencyUnitID)
	}
	if len(value) > 19 {
		return 0, currencyUnitIDRequestError(walletstore.ErrInvalidCurrencyUnitID)
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, currencyUnitIDRequestError(walletstore.ErrInvalidCurrencyUnitID)
	}
	return id, nil
}

func currencyUnitIDRequestError(err error) error {
	return apperr.Wrap(err, apperr.ErrBadRequest, err.Error())
}

func rejectQuoteRoundingMode(c *fiber.Ctx) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &payload); err != nil {
		return apperr.Wrap(err, apperr.ErrBadRequest, err.Error())
	}
	if _, present := payload["rounding_mode"]; present {
		return apperr.Wrap(errClientControlledQuoteRounding, apperr.ErrBadRequest, errClientControlledQuoteRounding.Error())
	}
	return nil
}

func (h *GRPCUserHandler) GetConversionQuote(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	outgoing, tenantID, userID, err := h.moneyRequestContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetConversionQuotePublic(outgoing, &walletv1.GetConversionQuotePublicRequest{
		TenantId: tenantID,
		UserId:   userID,
		QuoteId:  c.Params("id"),
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, http.StatusOK, response)
}

func (h *GRPCUserHandler) ListFXSources(c *fiber.Ctx) error {
	if !h.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	outgoing, tenantID, _, err := h.moneyRequestContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.ListFXSourcesPublic(outgoing, &walletv1.ListFXSourcesPublicRequest{TenantId: tenantID})
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, http.StatusOK, response)
}

func (h *GRPCUserHandler) bindMoneyRequest(c *fiber.Ctx, destination any) error {
	if !h.Config.WalletEnabled {
		return apperr.ErrUnavailable
	}
	if err := bindStrictJSON(c, destination); err != nil {
		return err
	}
	if err := rejectJSONField(c, "tenant_id"); err != nil {
		return err
	}
	if err := rejectJSONField(c, "user_id"); err != nil {
		return err
	}
	return nil
}

func (h *GRPCUserHandler) moneyRequestContext(c *fiber.Ctx) (context.Context, string, int64, error) {
	if err := rejectTenantIDQuery(c); err != nil {
		return nil, "", 0, err
	}
	if c.Request().URI().QueryArgs().Has("user_id") {
		return nil, "", 0, apperr.Wrap(errWalletTenantOverride, apperr.ErrBadRequest, "user_id is not accepted on wallet user routes")
	}
	userID, err := authenticatedUserID(c)
	if err != nil {
		return nil, "", 0, err
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return nil, "", 0, err
	}
	outgoing, err := walletOutgoingContext(c, tenantID, userID)
	if err != nil {
		return nil, "", 0, err
	}
	return outgoing, tenantID, userID, nil
}

func protoJSONResponse(c *fiber.Ctx, statusCode int, message proto.Message) error {
	payload, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(message)
	if err != nil {
		return jsonResponse(c, http.StatusInternalServerError, apperr.Wrap(err, apperr.ErrInternal, "encode wallet response"))
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(statusCode).Send(payload)
}
