package handler

import (
	"net/http"

	"github.com/adonese/noebs/apperr"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/protobuf/encoding/protojson"
)

func (h *GRPCUserHandler) previewP2P(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	r := &walletv1.PreviewP2PTransferRequest{}
	if len(c.Body()) > 8192 || (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(c.Body(), r) != nil || r.TenantId != "" {
		return jsonResponse(c, http.StatusBadRequest, apperr.ErrBadRequest)
	}
	r.TenantId = tenant
	response, err := h.Client.PreviewP2PTransfer(ctx, r)
	return publicWalletResponse(c, response, err, http.StatusOK)
}

func (h *GRPCUserHandler) p2pStatus(c *fiber.Ctx) error {
	ctx, tenant, err := h.publicWalletContext(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	response, err := h.Client.GetP2PTransferStatus(ctx, &walletv1.GetP2PTransferStatusRequest{TenantId: tenant, IdempotencyKey: c.Query("idempotency_key")})
	return publicWalletResponse(c, response, err, http.StatusOK)
}
