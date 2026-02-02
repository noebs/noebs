package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/wallet"
	"github.com/adonese/noebs/wallet/rbac"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

type AdminHandler struct {
	Service  *wallet.Service
	Temporal TemporalClient
}

type TemporalClient interface {
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
}

func NewAdminHandler(service *wallet.Service, temporal TemporalClient) *AdminHandler {
	return &AdminHandler{Service: service, Temporal: temporal}
}

type withdrawalApprovalPayload struct {
	WalletID         string `json:"wallet_id"`
	OwnerType        string `json:"owner_type"`
	OwnerID          string `json:"owner_id"`
	DestinationID    int64  `json:"destination_id"`
	ApprovalRequired bool   `json:"approval_required"`
}

func (h *AdminHandler) Dashboard(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	return renderComponent(c, http.StatusOK, WalletDashboardPage(WalletDashboardView{TenantID: tenantID}))
}

func (h *AdminHandler) ListWallets(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit, offset, err := parseLimitOffset(c, 50)
	if err != nil {
		return jsonResponse(c, 0, err)
	}

	wallets, err := h.Service.Store.ListWallets(c.Context(), tenantID, limit, offset)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := WalletListView{TenantID: tenantID, Wallets: wallets}
	return renderComponent(c, http.StatusOK, WalletListPage(view))
}

func (h *AdminHandler) WalletDetail(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	walletID, err := parseWalletID(c.Params("id"))
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid wallet id"))
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	walletModel, err := h.Service.Store.GetWallet(c.Context(), tenantID, walletID)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	fundingSources, err := h.Service.Store.ListFundingSources(c.Context(), tenantID, walletID)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	destinations, err := h.Service.Store.ListWithdrawalDestinations(c.Context(), tenantID, walletID, false)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := WalletDetailView{
		TenantID:       tenantID,
		Wallet:         *walletModel,
		FundingSources: fundingSources,
		Destinations:   destinations,
	}
	return renderComponent(c, http.StatusOK, WalletDetailPage(view))
}

func (h *AdminHandler) PendingApprovals(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit, offset, err := parseLimitOffset(c, 50)
	if err != nil {
		return jsonResponse(c, 0, err)
	}

	manualTransfers, err := h.Service.Store.ListManualTransfersByStatus(c.Context(), tenantID, walletworkflow.ManualTransferStatusPending, limit, offset)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	withdrawalTxns, err := h.Service.Store.ListPendingWithdrawalApprovals(c.Context(), tenantID, limit, offset)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	withdrawals := make([]WithdrawalApprovalItem, 0, len(withdrawalTxns))
	for _, txn := range withdrawalTxns {
		if !txn.WorkflowID.Valid {
			continue
		}
		withdrawals = append(withdrawals, buildWithdrawalApproval(txn))
	}
	view := PendingApprovalsView{
		TenantID:        tenantID,
		ManualTransfers: manualTransfers,
		Withdrawals:     withdrawals,
	}
	return renderComponent(c, http.StatusOK, PendingApprovalsPage(view))
}

func (h *AdminHandler) ApproveTransfer(c *fiber.Ctx) error {
	return h.handleDecision(c, true)
}

func (h *AdminHandler) RejectTransfer(c *fiber.Ctx) error {
	return h.handleDecision(c, false)
}

func (h *AdminHandler) AuditLog(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit, offset, err := parseLimitOffset(c, 100)
	if err != nil {
		return jsonResponse(c, 0, err)
	}

	filter := walletstore.AuditLogFilter{
		TenantID:   tenantID,
		EventType:  strings.TrimSpace(c.Query("event_type")),
		ActorType:  strings.TrimSpace(c.Query("actor_type")),
		ActorID:    strings.TrimSpace(c.Query("actor_id")),
		TargetType: strings.TrimSpace(c.Query("target_type")),
		TargetID:   strings.TrimSpace(c.Query("target_id")),
		Action:     strings.TrimSpace(c.Query("action")),
		Limit:      limit,
		Offset:     offset,
	}
	startStr := strings.TrimSpace(c.Query("start"))
	endStr := strings.TrimSpace(c.Query("end"))
	if startStr != "" || endStr != "" {
		if startStr == "" {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingStartTime))
		}
		if endStr == "" {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingEndTime))
		}
		start, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid start time"))
		}
		end, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid end time"))
		}
		filter.Start = start
		filter.End = end
	}
	events, err := h.Service.Store.ListAuditEvents(c.Context(), filter)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := AuditLogView{
		TenantID: tenantID,
		Events:   events,
		Filter: AuditFilterView{
			EventType:  filter.EventType,
			ActorType:  filter.ActorType,
			ActorID:    filter.ActorID,
			TargetType: filter.TargetType,
			TargetID:   filter.TargetID,
			Action:     filter.Action,
			Start:      startStr,
			End:        endStr,
			Limit:      limit,
			Offset:     offset,
		},
	}
	return renderComponent(c, http.StatusOK, AuditLogPage(view))
}

func (h *AdminHandler) Transactions(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit, offset, err := parseLimitOffset(c, 100)
	if err != nil {
		return jsonResponse(c, 0, err)
	}

	filter := walletstore.PSPTransactionFilter{
		TenantID:        tenantID,
		Status:          strings.TrimSpace(c.Query("status")),
		Provider:        strings.TrimSpace(c.Query("provider")),
		Direction:       strings.TrimSpace(c.Query("direction")),
		ClientReference: strings.TrimSpace(c.Query("client_reference")),
		Limit:           limit,
		Offset:          offset,
	}
	startStr := strings.TrimSpace(c.Query("start"))
	endStr := strings.TrimSpace(c.Query("end"))
	if startStr != "" || endStr != "" {
		if startStr == "" {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingStartTime))
		}
		if endStr == "" {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingEndTime))
		}
		start, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid start time"))
		}
		end, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid end time"))
		}
		filter.Start = start
		filter.End = end
	}

	txns, err := h.Service.Store.ListPSPTransactions(c.Context(), filter)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := PSPTransactionsView{
		TenantID:     tenantID,
		Transactions: txns,
		Filter: PSPTransactionFilterView{
			Status:    filter.Status,
			Provider:  filter.Provider,
			Direction: filter.Direction,
			ClientRef: filter.ClientReference,
			Start:     startStr,
			End:       endStr,
			Limit:     limit,
			Offset:    offset,
		},
	}
	return renderComponent(c, http.StatusOK, PSPTransactionsPage(view))
}

func (h *AdminHandler) TransactionDetail(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	clientRef := strings.TrimSpace(c.Params("client_reference"))
	if clientRef == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingClientReference))
	}
	txn, err := h.Service.Store.GetPSPTransactionByReference(c.Context(), tenantID, clientRef)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := PSPTransactionDetailView{
		TenantID:    tenantID,
		Transaction: *txn,
	}
	return renderComponent(c, http.StatusOK, PSPTransactionDetailPage(view))
}

func (h *AdminHandler) ManualTransfers(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit, offset, err := parseLimitOffset(c, 50)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	filter := walletstore.ManualTransferFilter{
		TenantID:     tenantID,
		Status:       strings.TrimSpace(c.Query("status")),
		TransferType: strings.TrimSpace(c.Query("transfer_type")),
		WalletID:     strings.TrimSpace(c.Query("wallet_id")),
		Limit:        limit,
		Offset:       offset,
	}
	requestedByStr := strings.TrimSpace(c.Query("requested_by"))
	if requestedByStr != "" {
		requestedBy, err := strconv.ParseInt(requestedByStr, 10, 64)
		if err != nil || requestedBy <= 0 {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid requested_by"))
		}
		filter.RequestedBy = requestedBy
	}
	startStr := strings.TrimSpace(c.Query("start"))
	endStr := strings.TrimSpace(c.Query("end"))
	if startStr != "" || endStr != "" {
		if startStr == "" {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingStartTime))
		}
		if endStr == "" {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingEndTime))
		}
		start, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid start time"))
		}
		end, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid end time"))
		}
		filter.Start = start
		filter.End = end
	}
	transfers, err := h.Service.Store.ListManualTransfers(c.Context(), filter)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := ManualTransferFormView{
		TenantID:  tenantID,
		Transfers: transfers,
		Filter: ManualTransferFilterView{
			Status:       filter.Status,
			TransferType: filter.TransferType,
			WalletID:     filter.WalletID,
			RequestedBy:  requestedByStr,
			Start:        startStr,
			End:          endStr,
			Limit:        limit,
			Offset:       offset,
		},
		Values: ManualTransferFormValues{
			Currency: h.Service.Config.WalletDefaultCurrency,
		},
	}
	return renderComponent(c, http.StatusOK, ManualTransferFormPage(view))
}

func (h *AdminHandler) SubmitManualTransfer(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if h.Temporal == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	tenantID, err := resolveTenantID(h.Service.Config, strings.TrimSpace(c.FormValue("tenant_id")))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	idempotencyKey := strings.TrimSpace(c.FormValue("idempotency_key"))
	if idempotencyKey == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingIdempotencyKey))
	}
	transferType := strings.TrimSpace(c.FormValue("transfer_type"))
	if transferType == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingTransferType))
	}
	walletID := strings.TrimSpace(c.FormValue("wallet_id"))
	if walletID == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingWalletID))
	}
	if _, err := uuid.Parse(walletID); err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid wallet id"))
	}
	amountRaw := strings.TrimSpace(c.FormValue("amount"))
	if amountRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
	}
	amount, err := strconv.ParseInt(amountRaw, 10, 64)
	if err != nil || amount <= 0 {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
	}
	currency, err := resolveCurrency(h.Service.Config, strings.TrimSpace(c.FormValue("currency")))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	reason := strings.TrimSpace(c.FormValue("reason"))
	if reason == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingReason))
	}
	requestedBy, err := parseInt64Field(c, "requested_by")
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if requestedBy <= 0 {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingApproverID))
	}
	pspProvider := strings.TrimSpace(c.FormValue("psp_provider"))
	pspReference := strings.TrimSpace(c.FormValue("psp_reference"))
	approvalTTL := strings.TrimSpace(c.FormValue("approval_timeout_seconds"))
	approvalTimeoutSeconds := 0
	if approvalTTL != "" {
		parsed, err := strconv.Atoi(approvalTTL)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid approval timeout"))
		}
		approvalTimeoutSeconds = parsed
	}
	if approvalTimeoutSeconds <= 0 {
		approvalTimeoutSeconds = h.Service.Config.WalletManualTransferApprovalTimeoutSeconds
	}
	if approvalTimeoutSeconds <= 0 {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingApprovalTimeout))
	}

	ctx := c.UserContext()
	if ctx == nil {
		ctx = context.Background()
	}
	workflowID := manualTransferWorkflowID(tenantID, idempotencyKey)
	params := walletworkflow.ManualTransferParams{
		TenantID:               tenantID,
		IdempotencyKey:         idempotencyKey,
		TransferType:           transferType,
		WalletID:               walletID,
		Amount:                 amount,
		Currency:               currency,
		Reason:                 reason,
		RequestedBy:            requestedBy,
		PSPProvider:            pspProvider,
		PSPReference:           pspReference,
		ApprovalTimeoutSeconds: approvalTimeoutSeconds,
	}
	_, err = h.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             string(walletworker.TaskQueueMain),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.ManualTransfer, params)
	if err != nil {
		if already, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); ok {
			_ = already
		} else {
			return jsonResponse(c, http.StatusInternalServerError, apperr.Wrap(err, apperr.ErrInternal, err.Error()))
		}
	}
	redirect := "/admin/wallet/pending"
	if tenantID != "" {
		redirect += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	return c.Redirect(redirect, http.StatusSeeOther)
}

func (h *AdminHandler) ManualTransferDetail(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	workflowID := strings.TrimSpace(c.Params("workflow_id"))
	if workflowID == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingWorkflowID))
	}
	transfer, err := h.Service.Store.GetManualTransferByWorkflow(c.Context(), tenantID, workflowID)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	approvals, err := h.Service.Store.ListManualTransferApprovals(c.Context(), tenantID, transfer.ID)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := ManualTransferDetailView{
		TenantID:  tenantID,
		Transfer:  *transfer,
		Approvals: approvals,
	}
	return renderComponent(c, http.StatusOK, ManualTransferDetailPage(view))
}

func (h *AdminHandler) Fees(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if err := requirePermission(c, rbac.PermManageConfig); err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit, offset, err := parseLimitOffset(c, 100)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	filter := walletstore.FeeConfigFilter{
		TenantID:        tenantID,
		TransactionType: strings.TrimSpace(c.Query("transaction_type")),
		Currency:        strings.TrimSpace(c.Query("currency")),
		ActiveOnly:      c.Query("active_only") == "on" || c.Query("active_only") == "true",
		Limit:           limit,
		Offset:          offset,
	}
	cfgs, err := h.Service.Store.ListFeeConfigs(c.Context(), filter)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := FeeConfigView{
		TenantID: tenantID,
		Configs:  cfgs,
		Filter: FeeConfigFilterView{
			TransactionType: filter.TransactionType,
			Currency:        filter.Currency,
			ActiveOnly:      filter.ActiveOnly,
			Limit:           limit,
			Offset:          offset,
		},
		Form: FeeConfigFormValues{
			Currency: h.Service.Config.WalletDefaultCurrency,
			IsActive: true,
		},
	}
	return renderComponent(c, http.StatusOK, FeesPage(view))
}

func (h *AdminHandler) CreateFeeConfig(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if err := requirePermission(c, rbac.PermManageConfig); err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := resolveTenantID(h.Service.Config, strings.TrimSpace(c.FormValue("tenant_id")))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	txType := strings.TrimSpace(c.FormValue("transaction_type"))
	if txType == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingTransactionType))
	}
	currency, err := resolveCurrency(h.Service.Config, strings.TrimSpace(c.FormValue("currency")))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	tierMinRaw := strings.TrimSpace(c.FormValue("tier_min"))
	if tierMinRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
	}
	tierMin, err := strconv.ParseInt(tierMinRaw, 10, 64)
	if err != nil || tierMin < 0 {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
	}
	tierMaxRaw := strings.TrimSpace(c.FormValue("tier_max"))
	tierMax := sql.NullInt64{}
	if tierMaxRaw != "" {
		value, err := strconv.ParseInt(tierMaxRaw, 10, 64)
		if err != nil || value < 0 {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
		}
		tierMax = sql.NullInt64{Int64: value, Valid: true}
	}
	percentageRaw := strings.TrimSpace(c.FormValue("percentage_fee"))
	if percentageRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidPercentage))
	}
	percentageFee, err := decimal.NewFromString(percentageRaw)
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid percentage fee"))
	}
	flatFee := int64(0)
	if raw := strings.TrimSpace(c.FormValue("flat_fee")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
		}
		flatFee = value
	}
	minFee := int64(0)
	if raw := strings.TrimSpace(c.FormValue("min_fee")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
		}
		minFee = value
	}
	maxFeeRaw := strings.TrimSpace(c.FormValue("max_fee"))
	maxFee := sql.NullInt64{}
	if maxFeeRaw != "" {
		value, err := strconv.ParseInt(maxFeeRaw, 10, 64)
		if err != nil || value < 0 {
			return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
		}
		maxFee = sql.NullInt64{Int64: value, Valid: true}
	}
	feeAccount := strings.TrimSpace(c.FormValue("fee_account_code"))
	isActive := c.FormValue("is_active") == "on" || c.FormValue("is_active") == "true"
	cfg := walletstore.FeeConfig{
		TenantID:        tenantID,
		TransactionType: txType,
		Currency:        currency,
		TierMin:         tierMin,
		TierMax:         tierMax,
		PercentageFee:   percentageFee,
		FlatFee:         flatFee,
		MinFee:          minFee,
		MaxFee:          maxFee,
		FeeAccountCode:  sql.NullString{String: feeAccount, Valid: feeAccount != ""},
		IsActive:        isActive,
	}
	if _, err := h.Service.Store.CreateFeeConfig(c.Context(), cfg); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	redirect := "/admin/wallet/fees"
	if tenantID != "" {
		redirect += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	return c.Redirect(redirect, http.StatusSeeOther)
}

func (h *AdminHandler) Rates(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if err := requirePermission(c, rbac.PermManageConfig); err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit, offset, err := parseLimitOffset(c, 100)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	filter := walletstore.ExchangeRateFilter{
		TenantID:      tenantID,
		BaseCurrency:  strings.TrimSpace(c.Query("base_currency")),
		QuoteCurrency: strings.TrimSpace(c.Query("quote_currency")),
		ActiveOnly:    c.Query("active_only") == "on" || c.Query("active_only") == "true",
		Limit:         limit,
		Offset:        offset,
	}
	rates, err := h.Service.Store.ListExchangeRates(c.Context(), filter)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	view := RateView{
		TenantID: tenantID,
		Rates:    rates,
		Filter: RateFilterView{
			BaseCurrency:  filter.BaseCurrency,
			QuoteCurrency: filter.QuoteCurrency,
			ActiveOnly:    filter.ActiveOnly,
			Limit:         limit,
			Offset:        offset,
		},
	}
	return renderComponent(c, http.StatusOK, RatesPage(view))
}

func (h *AdminHandler) CreateRate(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if err := requirePermission(c, rbac.PermManageConfig); err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := resolveTenantID(h.Service.Config, strings.TrimSpace(c.FormValue("tenant_id")))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	base := strings.TrimSpace(c.FormValue("base_currency"))
	if base == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingBaseCurrency))
	}
	quote := strings.TrimSpace(c.FormValue("quote_currency"))
	if quote == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingQuoteCurrency))
	}
	buyRaw := strings.TrimSpace(c.FormValue("buy_rate"))
	if buyRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidRate))
	}
	buyRate, err := decimal.NewFromString(buyRaw)
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid buy rate"))
	}
	sellRaw := strings.TrimSpace(c.FormValue("sell_rate"))
	if sellRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidRate))
	}
	sellRate, err := decimal.NewFromString(sellRaw)
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid sell rate"))
	}
	spreadRaw := strings.TrimSpace(c.FormValue("spread"))
	spread := decimal.NullDecimal{}
	if spreadRaw != "" {
		value, err := decimal.NewFromString(spreadRaw)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid spread"))
		}
		spread = decimal.NullDecimal{Decimal: value, Valid: true}
	}
	setBy := strings.TrimSpace(c.FormValue("set_by"))
	if setBy == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingSetBy))
	}
	effectiveRaw := strings.TrimSpace(c.FormValue("effective_from"))
	effectiveFrom := time.Now().UTC()
	if effectiveRaw != "" {
		parsed, err := time.Parse(time.RFC3339, effectiveRaw)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid effective_from"))
		}
		effectiveFrom = parsed
	}
	rate := walletstore.ExchangeRate{
		TenantID:      tenantID,
		BaseCurrency:  base,
		QuoteCurrency: quote,
		BuyRate:       buyRate,
		SellRate:      sellRate,
		Spread:        spread,
		SetBy:         setBy,
		EffectiveFrom: effectiveFrom,
	}
	if _, err := h.Service.Store.CreateExchangeRate(c.Context(), rate); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	redirect := "/admin/wallet/rates"
	if tenantID != "" {
		redirect += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	return c.Redirect(redirect, http.StatusSeeOther)
}

func (h *AdminHandler) handleDecision(c *fiber.Ctx, approved bool) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if h.Temporal == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	workflowID := strings.TrimSpace(c.Params("workflow_id"))
	if workflowID == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingWorkflowID))
	}
	kind := strings.TrimSpace(c.FormValue("kind"))
	if kind == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingDecision))
	}
	approverID, err := parseInt64Field(c, "approver_id")
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if approverID <= 0 {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingApproverID))
	}
	proof := strings.TrimSpace(c.FormValue("proof_of_payment"))
	if approved && proof == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingProofOfPayment))
	}
	reason := strings.TrimSpace(c.FormValue("reason"))
	if !approved && reason == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingReason))
	}

	ctx := c.UserContext()
	if ctx == nil {
		ctx = context.Background()
	}
	tenantID := strings.TrimSpace(c.FormValue("tenant_id"))
	if tenantID == "" {
		tenantID = strings.TrimSpace(c.Query("tenant_id"))
	}
	resolvedTenantID := tenantID
	if resolved, err := resolveTenantID(h.Service.Config, tenantID); err == nil {
		resolvedTenantID = resolved
	}
	var signalErr error
	switch kind {
	case "manual_transfer":
		if resolvedTenantID != "" {
			transfer, err := h.Service.Store.GetManualTransferByWorkflow(ctx, resolvedTenantID, workflowID)
			if err == nil && transfer.RequestedBy.Valid && transfer.RequestedBy.Int64 == approverID {
				return jsonResponse(c, 0, mapWalletError(walletstore.ErrApproverIsRequester))
			}
			if err != nil && !errors.Is(err, walletstore.ErrManualTransferNotFound) && !errors.Is(err, walletstore.ErrMissingTenantID) {
				return jsonResponse(c, 0, mapWalletError(err))
			}
		}
		decision := walletworkflow.ManualTransferDecision{
			Approved:       approved,
			ApproverID:     approverID,
			Reason:         reason,
			ProofOfPayment: proof,
		}
		signalErr = h.Temporal.SignalWorkflow(ctx, workflowID, "", walletworkflow.ManualTransferDecisionSignal, decision)
	case "withdrawal":
		decision := walletworkflow.WithdrawalApprovalDecision{
			Approved:       approved,
			ApproverID:     approverID,
			Reason:         reason,
			ProofOfPayment: proof,
		}
		signalErr = h.Temporal.SignalWorkflow(ctx, workflowID, "", walletworkflow.WithdrawalApprovalSignal, decision)
	default:
		return jsonResponse(c, 0, apperr.Wrap(walletstore.ErrMissingDecision, apperr.ErrBadRequest, "unsupported decision kind"))
	}
	if signalErr != nil {
		var notFound *serviceerror.NotFound
		if errors.As(signalErr, &notFound) {
			return jsonResponse(c, http.StatusNotFound, apperr.Wrap(signalErr, apperr.ErrNotFound, signalErr.Error()))
		}
		return jsonResponse(c, http.StatusInternalServerError, apperr.Wrap(signalErr, apperr.ErrInternal, signalErr.Error()))
	}

	redirect := "/admin/wallet/pending"
	if tenantID != "" {
		redirect += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	return c.Redirect(redirect, http.StatusSeeOther)
}

func buildWithdrawalApproval(txn walletstore.PSPTransaction) WithdrawalApprovalItem {
	item := WithdrawalApprovalItem{
		WorkflowID:  txn.WorkflowID.String,
		ClientRef:   txn.ClientReference,
		Amount:      txn.Amount,
		Currency:    txn.Currency,
		Provider:    txn.PSPProvider,
		Status:      txn.Status,
		RequestedAt: txn.CreatedAt,
	}
	if txn.WorkflowID.Valid {
		item.WorkflowID = txn.WorkflowID.String
	}
	if len(txn.RawRequest) > 0 {
		var payload withdrawalApprovalPayload
		if err := json.Unmarshal(txn.RawRequest, &payload); err == nil {
			item.WalletID = payload.WalletID
			item.OwnerType = payload.OwnerType
			item.OwnerID = payload.OwnerID
			item.DestinationID = payload.DestinationID
			item.ApprovalNeeded = payload.ApprovalRequired
		}
	}
	return item
}

func manualTransferWorkflowID(tenantID, idempotencyKey string) string {
	if tenantID == "" {
		return "wallet-manual-" + idempotencyKey
	}
	return "wallet-manual-" + tenantID + "-" + idempotencyKey
}

func parseLimitOffset(c *fiber.Ctx, defaultLimit int) (int, int, error) {
	limit := defaultLimit
	offset := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid limit")
		}
		if parsed <= 0 {
			return 0, 0, apperr.Wrap(walletstore.ErrInvalidLimit, apperr.ErrBadRequest, "invalid limit")
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid offset")
		}
		if parsed < 0 {
			return 0, 0, apperr.Wrap(walletstore.ErrInvalidOffset, apperr.ErrBadRequest, "invalid offset")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func parseWalletID(raw string) (uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return uuid.Nil, walletstore.ErrMissingWalletID
	}
	walletID, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, err
	}
	return walletID, nil
}

func parseInt64Field(c *fiber.Ctx, key string) (int64, error) {
	raw := strings.TrimSpace(c.FormValue(key))
	if raw == "" {
		return 0, apperr.Wrap(walletstore.ErrMissingApproverID, apperr.ErrBadRequest, "missing value")
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid value")
	}
	return value, nil
}
