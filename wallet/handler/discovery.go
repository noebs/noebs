package handler

import (
	"context"
	"net/http"

	"github.com/adonese/noebs/apperr"
	"google.golang.org/protobuf/proto"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/gofiber/fiber/v2"
)

func (h *GRPCUserHandler) walletAccount(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetWalletAccount(ctx, &walletv1.GetWalletAccountRequest{TenantId: tenant})
	return publicWalletResponse(c, response, err, http.StatusOK)
}

func (h *GRPCUserHandler) walletProviders(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.ListWalletProviders(ctx, &walletv1.ListWalletProvidersRequest{TenantId: tenant})
	return publicWalletResponse(c, response, err, http.StatusOK)
}

func (h *GRPCUserHandler) walletFundingMethods(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{
		TenantId: tenant, WalletId: c.Query("wallet_id"),
	})
	return publicWalletResponse(c, response, err, http.StatusOK)
}

// Every wallet view uses the same verified NoEBS customer and tenant boundary.
func (h *GRPCUserHandler) publicWalletContext(c *fiber.Ctx) (context.Context, string, error) {
	if !h.Config.WalletEnabled {
		return nil, "", apperr.ErrUnavailable
	}
	ctx, tenant, _, err := h.moneyRequestContext(c)
	return ctx, tenant, err
}

func publicWalletResponse(c *fiber.Ctx, result proto.Message, err error, code int) error {
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	return protoJSONResponse(c, code, result)
}
