package handler

import (
	"context"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/apperr"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc/metadata"
)

type GRPCAdminHandler struct {
	Client walletv1.WalletAdminServiceClient
}

func NewGRPCAdminHandler(client walletv1.WalletAdminServiceClient) *GRPCAdminHandler {
	return &GRPCAdminHandler{Client: client}
}

func RegisterGRPCAdminRoutes(router fiber.Router, handler *GRPCAdminHandler) {
	router.Get("", handler.Dashboard)
	router.Get("/", handler.Dashboard)
	router.Get("/wallets", handler.ListWallets)
	router.Get("/wallets/:id", handler.WalletDetail)
	router.Get("/transactions", handler.Transactions)
	router.Get("/transactions/:client_reference", handler.TransactionDetail)
	router.Get("/pending", handler.PendingApprovals)
	router.Get("/manual", handler.ManualTransfers)
	router.Post("/manual", handler.SubmitManualTransfer)
	router.Get("/manual/:workflow_id", handler.ManualTransferDetail)
	router.Get("/fees", handler.Fees)
	router.Post("/fees", handler.CreateFeeConfig)
	router.Get("/rates", handler.Rates)
	router.Post("/rates", handler.CreateRate)
	router.Post("/approve/:workflow_id", handler.ApproveTransfer)
	router.Post("/reject/:workflow_id", handler.RejectTransfer)
	router.Get("/audit", handler.AuditLog)
}

func (h *GRPCAdminHandler) Dashboard(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_DASHBOARD, nil)
}

func (h *GRPCAdminHandler) ListWallets(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_WALLETS, nil)
}

func (h *GRPCAdminHandler) WalletDetail(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_WALLET_DETAIL, fiber.Map{"id": c.Params("id")})
}

func (h *GRPCAdminHandler) PendingApprovals(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_PENDING_APPROVALS, nil)
}

func (h *GRPCAdminHandler) AuditLog(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_AUDIT_EVENTS, nil)
}

func (h *GRPCAdminHandler) Transactions(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_TRANSACTIONS, nil)
}

func (h *GRPCAdminHandler) TransactionDetail(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_TRANSACTION_DETAIL, fiber.Map{"client_reference": c.Params("client_reference")})
}

func (h *GRPCAdminHandler) ManualTransfers(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_MANUAL_TRANSFERS, nil)
}

func (h *GRPCAdminHandler) SubmitManualTransfer(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_SUBMIT_MANUAL_TRANSFER, nil)
}

func (h *GRPCAdminHandler) ManualTransferDetail(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_MANUAL_TRANSFER_DETAIL, fiber.Map{"workflow_id": c.Params("workflow_id")})
}

func (h *GRPCAdminHandler) Fees(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_FEES, nil)
}

func (h *GRPCAdminHandler) CreateFeeConfig(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_FEE, nil)
}

func (h *GRPCAdminHandler) Rates(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_RATES, nil)
}

func (h *GRPCAdminHandler) CreateRate(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_RATE, nil)
}

func (h *GRPCAdminHandler) ApproveTransfer(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_APPROVE_TRANSFER, fiber.Map{"workflow_id": c.Params("workflow_id")})
}

func (h *GRPCAdminHandler) RejectTransfer(c *fiber.Ctx) error {
	return h.render(c, walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_REJECT_TRANSFER, fiber.Map{"workflow_id": c.Params("workflow_id")})
}

func (h *GRPCAdminHandler) render(c *fiber.Ctx, action walletv1.AdminWalletAction, path fiber.Map) error {
	if err := authenticatedAdminIdentity(c); err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := requirePermission(c, adminActionPermission(action)); err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	req := &walletv1.RenderWalletAdminRequest{
		Action: action,
		Query:  queryArgs(c),
		Form:   formArgs(c),
		Path:   stringMap(path),
	}
	outgoing, err := adminOutgoingContext(c, tenantID)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	resp, err := h.Client.RenderWalletAdmin(outgoing, req)
	if err != nil {
		return jsonResponse(c, 0, mapWalletGRPCError(err))
	}
	statusCode := int(resp.GetStatusCode())
	if resp.GetRedirectLocation() != "" {
		return c.Redirect(resp.GetRedirectLocation(), statusCode)
	}
	if resp.GetContentType() != "" {
		c.Set(fiber.HeaderContentType, resp.GetContentType())
	}
	return c.Status(statusCode).Send(resp.GetBody())
}

func adminOutgoingContext(c *fiber.Ctx, tenantID string) (context.Context, error) {
	principal, ok := gateway.InternalPrincipalIdentity(c)
	if !ok || principal.TenantID != tenantID {
		return nil, apperr.ErrUnauthorized
	}
	values := c.Request().Header.PeekAll(backofficeauth.HeaderCSRFToken)
	if len(values) != 1 {
		return nil, apperr.ErrUnauthorized
	}
	csrfToken := string(values[0])
	if err := backofficeauth.ValidateCSRFToken(csrfToken); err != nil {
		return nil, apperr.ErrUnauthorized
	}
	ctx := principalOutgoingContext(c.UserContext(), principal)
	return metadata.AppendToOutgoingContext(ctx, strings.ToLower(backofficeauth.HeaderCSRFToken), csrfToken), nil
}

func adminActionPermission(action walletv1.AdminWalletAction) tenantauth.Permission {
	switch action {
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_AUDIT_EVENTS:
		return tenantauth.PermissionWalletAuditRead
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_SUBMIT_MANUAL_TRANSFER:
		return tenantauth.PermissionWalletManualCreate
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_FEE:
		return tenantauth.PermissionWalletFeesWrite
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_RATE:
		return tenantauth.PermissionWalletRatesWrite
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_APPROVE_TRANSFER:
		return tenantauth.PermissionWalletWorkflowApprove
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_REJECT_TRANSFER:
		return tenantauth.PermissionWalletWorkflowReject
	default:
		return tenantauth.PermissionWalletRead
	}
}

func queryArgs(c *fiber.Ctx) map[string]string {
	values := map[string]string{}
	c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		values[string(key)] = string(value)
	})
	return values
}

func formArgs(c *fiber.Ctx) map[string]string {
	values := map[string]string{}
	c.Request().PostArgs().VisitAll(func(key, value []byte) {
		values[string(key)] = string(value)
	})
	return values
}

func stringMap(values fiber.Map) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		if str, ok := value.(string); ok {
			out[key] = str
		}
	}
	return out
}
