package handler

import (
	"net/http"

	"github.com/adonese/noebs/apperr"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/transactionauth"
	walletrequest "github.com/adonese/noebs/wallet/request"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/protobuf/encoding/protojson"
)

func registerInteropRoutes(router fiber.Router, h *GRPCUserHandler) {
	router.Get("/interop", h.interopCapability)
	router.Post("/interop/quotes", h.interopCreateQuote)
	router.Get("/interop/quotes/:id", h.interopGetQuote)
	router.Post("/interop/quotes/:id/close", h.interopCloseQuote)
	router.Post("/interop/transfers", h.interopRequestTransfer)
	router.Get("/interop/transfers", h.interopGetTransfer)
	router.Get("/interop/transfers/:id", h.interopGetTransfer)
}
func (h *GRPCUserHandler) interopCapability(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetInteropCapability(ctx, &walletv1.GetInteropCapabilityRequest{TenantId: tenant})
	return publicWalletResponse(c, response, err, http.StatusOK)
}
func (h *GRPCUserHandler) interopCreateQuote(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	r := &walletv1.CreateInteropQuoteRequest{}
	if len(c.Body()) > 8192 || (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(c.Body(), r) != nil || r.TenantId != "" {
		return jsonResponse(c, http.StatusBadRequest, apperr.ErrBadRequest)
	}
	r.TenantId = tenant
	response, err := h.Client.CreateInteropQuote(ctx, r)
	return publicWalletResponse(c, response, err, http.StatusAccepted)
}
func (h *GRPCUserHandler) interopGetQuote(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetInteropQuote(ctx, &walletv1.GetInteropQuoteRequest{TenantId: tenant, QuoteId: c.Params("id")})
	return publicWalletResponse(c, response, err, http.StatusOK)
}
func (h *GRPCUserHandler) interopCloseQuote(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.CloseInteropQuote(ctx, &walletv1.GetInteropQuoteRequest{TenantId: tenant, QuoteId: c.Params("id")})
	return publicWalletResponse(c, response, err, http.StatusOK)
}
func (h *GRPCUserHandler) interopRequestTransfer(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	canonical, err := walletrequest.ParseCanonical(transactionauth.OperationWalletInterop, tenant, c.Body())
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, apperr.ErrBadRequest)
	}
	response, err := h.Client.RequestInteropTransfer(ctx, canonical.Message.(*walletv1.RequestInteropTransferRequest))
	return publicWalletResponse(c, response, err, http.StatusAccepted)
}
func (h *GRPCUserHandler) interopGetTransfer(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetInteropTransfer(ctx, &walletv1.GetInteropTransferRequest{TenantId: tenant, TransferId: c.Params("id"), IdempotencyKey: c.Query("idempotency_key")})
	return publicWalletResponse(c, response, err, http.StatusOK)
}
