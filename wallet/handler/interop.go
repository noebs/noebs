package handler

import (
	"context"
	"net/http"

	"github.com/adonese/noebs/apperr"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/transactionauth"
	walletrequest "github.com/adonese/noebs/wallet/request"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func registerInteropRoutes(router fiber.Router, h *GRPCUserHandler) {
	router.Get("/interop", h.interopCapability)
	router.Post("/interop/quotes", h.interopCreateQuote)
	router.Get("/interop/quotes/:id", h.interopGetQuote)
	router.Post("/interop/transfers", h.interopRequestTransfer)
	router.Get("/interop/transfers", h.interopGetTransfer)
	router.Get("/interop/transfers/:id", h.interopGetTransfer)
}
func (h *GRPCUserHandler) interopContext(c *fiber.Ctx) (context.Context, string, error) {
	if !h.Config.WalletEnabled {
		return nil, "", apperr.ErrUnavailable
	}
	user, err := authenticatedUserID(c)
	if err != nil {
		return nil, "", err
	}
	tenant, err := authenticatedTenantID(c)
	if err != nil {
		return nil, "", err
	}
	ctx, err := walletOutgoingContext(c, tenant, user)
	return ctx, tenant, err
}
func interopHTTPResponse(c *fiber.Ctx, result proto.Message, err error, code int) error {
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(result)
	if err != nil {
		return jsonResponse(c, 0, apperr.ErrInternal)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Status(code).Send(body)
}
func (h *GRPCUserHandler) interopCapability(c *fiber.Ctx) error {
	ctx, tenant, err := h.interopContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetInteropCapability(ctx, &walletv1.GetInteropCapabilityRequest{TenantId: tenant})
	return interopHTTPResponse(c, response, err, http.StatusOK)
}
func (h *GRPCUserHandler) interopCreateQuote(c *fiber.Ctx) error {
	ctx, tenant, err := h.interopContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	r := &walletv1.CreateInteropQuoteRequest{}
	if len(c.Body()) > 8192 || (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(c.Body(), r) != nil || r.TenantId != "" {
		return jsonResponse(c, http.StatusBadRequest, apperr.ErrBadRequest)
	}
	r.TenantId = tenant
	response, err := h.Client.CreateInteropQuote(ctx, r)
	return interopHTTPResponse(c, response, err, http.StatusAccepted)
}
func (h *GRPCUserHandler) interopGetQuote(c *fiber.Ctx) error {
	ctx, tenant, err := h.interopContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetInteropQuote(ctx, &walletv1.GetInteropQuoteRequest{TenantId: tenant, QuoteId: c.Params("id")})
	return interopHTTPResponse(c, response, err, http.StatusOK)
}
func (h *GRPCUserHandler) interopRequestTransfer(c *fiber.Ctx) error {
	ctx, tenant, err := h.interopContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	canonical, err := walletrequest.ParseCanonical(transactionauth.OperationWalletInterop, tenant, c.Body())
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, apperr.ErrBadRequest)
	}
	response, err := h.Client.RequestInteropTransfer(ctx, canonical.Message.(*walletv1.RequestInteropTransferRequest))
	return interopHTTPResponse(c, response, err, http.StatusAccepted)
}
func (h *GRPCUserHandler) interopGetTransfer(c *fiber.Ctx) error {
	ctx, tenant, err := h.interopContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetInteropTransfer(ctx, &walletv1.GetInteropTransferRequest{TenantId: tenant, TransferId: c.Params("id"), IdempotencyKey: c.Query("idempotency_key")})
	return interopHTTPResponse(c, response, err, http.StatusOK)
}
