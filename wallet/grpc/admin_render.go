package walletgrpc

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	wallethandler "github.com/adonese/noebs/wallet/handler"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const adminHTMLContentType = "text/html; charset=utf-8"

func (s *Server) RenderWalletAdmin(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	switch req.Action {
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_DASHBOARD:
		return s.renderAdminDashboard(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_WALLETS:
		return s.renderAdminWallets(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_WALLET_DETAIL:
		return s.renderAdminWalletDetail(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_PENDING_APPROVALS:
		return s.renderAdminPendingApprovals(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_AUDIT_EVENTS:
		return s.renderAdminAudit(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_TRANSACTIONS:
		return s.renderAdminTransactions(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_TRANSACTION_DETAIL:
		return s.renderAdminTransactionDetail(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_MANUAL_TRANSFERS:
		return s.renderAdminManualTransfers(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_SUBMIT_MANUAL_TRANSFER:
		return s.submitAdminManualTransfer(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_MANUAL_TRANSFER_DETAIL:
		return s.renderAdminManualTransferDetail(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_FEES:
		return s.renderAdminFees(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_FEE:
		return s.createAdminFee(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_LIST_RATES:
		return s.renderAdminRates(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_RATE:
		return s.createAdminRate(ctx, req)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_APPROVE_TRANSFER:
		return s.signalAdminDecision(ctx, req, true)
	case walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_REJECT_TRANSFER:
		return s.signalAdminDecision(ctx, req, false)
	default:
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDecision.Error())
	}
}

func (s *Server) renderAdminDashboard(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return adminHTML(ctx, wallethandler.WalletDashboardPage(wallethandler.WalletDashboardView{TenantID: tenantID}))
}

func (s *Server) renderAdminWallets(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset, err := adminLimitOffset(req.Query, 50)
	if err != nil {
		return nil, err
	}
	wallets, err := s.Service.Store.ListWallets(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.WalletListPage(wallethandler.WalletListView{
		TenantID: tenantID,
		Wallets:  wallets,
	}))
}

func (s *Server) renderAdminWalletDetail(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	walletID, err := adminUUID(adminPath(req, "id"))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	walletModel, err := s.Service.Store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return nil, mapError(err)
	}
	fundingSources, err := s.Service.Store.ListFundingSources(ctx, tenantID, walletID)
	if err != nil {
		return nil, mapError(err)
	}
	destinations, err := s.Service.Store.ListWithdrawalDestinations(ctx, tenantID, walletID, false)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.WalletDetailPage(wallethandler.WalletDetailView{
		TenantID:       tenantID,
		Wallet:         *walletModel,
		FundingSources: fundingSources,
		Destinations:   destinations,
	}))
}

func (s *Server) renderAdminPendingApprovals(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset, err := adminLimitOffset(req.Query, 50)
	if err != nil {
		return nil, err
	}
	manualTransfers, err := s.Service.Store.ListManualTransfersByStatus(ctx, tenantID, walletworkflow.ManualTransferStatusPending, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	withdrawalTxns, err := s.Service.Store.ListPendingWithdrawalApprovals(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	withdrawals := make([]wallethandler.WithdrawalApprovalItem, 0, len(withdrawalTxns))
	for _, txn := range withdrawalTxns {
		if txn.WorkflowID.Valid {
			withdrawals = append(withdrawals, adminWithdrawalApproval(txn))
		}
	}
	return adminHTML(ctx, wallethandler.PendingApprovalsPage(wallethandler.PendingApprovalsView{
		TenantID:        tenantID,
		ManualTransfers: manualTransfers,
		Withdrawals:     withdrawals,
	}))
}

func (s *Server) renderAdminAudit(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset, err := adminLimitOffset(req.Query, 100)
	if err != nil {
		return nil, err
	}
	filter := walletstore.AuditLogFilter{
		TenantID:   tenantID,
		EventType:  adminQuery(req, "event_type"),
		ActorType:  adminQuery(req, "actor_type"),
		ActorID:    adminQuery(req, "actor_id"),
		TargetType: adminQuery(req, "target_type"),
		TargetID:   adminQuery(req, "target_id"),
		Action:     adminQuery(req, "action"),
		Limit:      limit,
		Offset:     offset,
	}
	start, end, startStr, endStr, err := adminTimeRange(req.Query)
	if err != nil {
		return nil, err
	}
	filter.Start = start
	filter.End = end
	events, err := s.Service.Store.ListAuditEvents(ctx, filter)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.AuditLogPage(wallethandler.AuditLogView{
		TenantID: tenantID,
		Events:   events,
		Filter: wallethandler.AuditFilterView{
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
	}))
}

func (s *Server) renderAdminTransactions(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset, err := adminLimitOffset(req.Query, 100)
	if err != nil {
		return nil, err
	}
	filter := walletstore.PSPTransactionFilter{
		TenantID:        tenantID,
		Status:          adminQuery(req, "status"),
		Provider:        adminQuery(req, "provider"),
		Direction:       adminQuery(req, "direction"),
		ClientReference: adminQuery(req, "client_reference"),
		Limit:           limit,
		Offset:          offset,
	}
	start, end, startStr, endStr, err := adminTimeRange(req.Query)
	if err != nil {
		return nil, err
	}
	filter.Start = start
	filter.End = end
	txns, err := s.Service.Store.ListPSPTransactions(ctx, filter)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.PSPTransactionsPage(wallethandler.PSPTransactionsView{
		TenantID:     tenantID,
		Transactions: txns,
		Filter: wallethandler.PSPTransactionFilterView{
			Status:    filter.Status,
			Provider:  filter.Provider,
			Direction: filter.Direction,
			ClientRef: filter.ClientReference,
			Start:     startStr,
			End:       endStr,
			Limit:     limit,
			Offset:    offset,
		},
	}))
}

func (s *Server) renderAdminTransactionDetail(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	clientRef := strings.TrimSpace(adminPath(req, "client_reference"))
	if clientRef == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingClientReference.Error())
	}
	txn, err := s.Service.Store.GetPSPTransactionByReference(ctx, tenantID, clientRef)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.PSPTransactionDetailPage(wallethandler.PSPTransactionDetailView{
		TenantID:    tenantID,
		Transaction: *txn,
	}))
}

func (s *Server) renderAdminManualTransfers(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset, err := adminLimitOffset(req.Query, 50)
	if err != nil {
		return nil, err
	}
	filter := walletstore.ManualTransferFilter{
		TenantID:     tenantID,
		Status:       adminQuery(req, "status"),
		TransferType: adminQuery(req, "transfer_type"),
		WalletID:     adminQuery(req, "wallet_id"),
		Limit:        limit,
		Offset:       offset,
	}
	requestedByStr := adminQuery(req, "requested_by")
	if requestedByStr != "" {
		requestedBy, err := strconv.ParseInt(requestedByStr, 10, 64)
		if err != nil || requestedBy <= 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid requested_by")
		}
		filter.RequestedBy = requestedBy
	}
	start, end, startStr, endStr, err := adminTimeRange(req.Query)
	if err != nil {
		return nil, err
	}
	filter.Start = start
	filter.End = end
	transfers, err := s.Service.Store.ListManualTransfers(ctx, filter)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.ManualTransferFormPage(wallethandler.ManualTransferFormView{
		TenantID:  tenantID,
		Transfers: transfers,
		Filter: wallethandler.ManualTransferFilterView{
			Status:       filter.Status,
			TransferType: filter.TransferType,
			WalletID:     filter.WalletID,
			RequestedBy:  requestedByStr,
			Start:        startStr,
			End:          endStr,
			Limit:        limit,
			Offset:       offset,
		},
		Values: wallethandler.ManualTransferFormValues{},
	}))
}

func (s *Server) submitAdminManualTransfer(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	form := req.Form
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	amount, err := adminPositiveInt64(form, "amount", walletstore.ErrInvalidAmount)
	if err != nil {
		return nil, err
	}
	requestedBy, err := adminPositiveInt64(form, "requested_by", walletstore.ErrMissingApproverID)
	if err != nil {
		return nil, err
	}
	approvalTimeoutSeconds, err := adminOptionalPositiveInt(form, "approval_timeout_seconds")
	if err != nil {
		return nil, err
	}
	if approvalTimeoutSeconds <= 0 {
		approvalTimeoutSeconds = s.Service.Config.WalletManualTransferApprovalTimeoutSeconds
	}
	if approvalTimeoutSeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalTimeout.Error())
	}
	currency, err := adminCurrency(adminForm(form, "currency"))
	if err != nil {
		return nil, mapError(err)
	}
	idempotencyKey := adminForm(form, "idempotency_key")
	transferType := adminForm(form, "transfer_type")
	walletID := adminForm(form, "wallet_id")
	reason := adminForm(form, "reason")
	switch {
	case idempotencyKey == "":
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingIdempotencyKey.Error())
	case transferType == "":
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTransferType.Error())
	case walletID == "":
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	case reason == "":
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingReason.Error())
	}
	if err := walletstore.ValidateManualTransferType(transferType); err != nil {
		return nil, mapError(err)
	}
	if _, err := adminUUID(walletID); err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	taskQueue, err := s.temporalTaskQueue()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
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
		PSPProvider:            adminForm(form, "psp_provider"),
		PSPReference:           adminForm(form, "psp_reference"),
		ApprovalTimeoutSeconds: approvalTimeoutSeconds,
	}
	_, err = temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             string(taskQueue),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.ManualTransfer, params)
	if err != nil {
		if _, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); !ok {
			return nil, mapTemporalError(err)
		}
	}
	return adminRedirect("/admin/wallet/pending", tenantID), nil
}

func (s *Server) renderAdminManualTransferDetail(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	workflowID := strings.TrimSpace(adminPath(req, "workflow_id"))
	if workflowID == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWorkflowID.Error())
	}
	transfer, err := s.Service.Store.GetManualTransferByWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return nil, mapError(err)
	}
	approvals, err := s.Service.Store.ListManualTransferApprovals(ctx, tenantID, transfer.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.ManualTransferDetailPage(wallethandler.ManualTransferDetailView{
		TenantID:  tenantID,
		Transfer:  *transfer,
		Approvals: approvals,
	}))
}

func (s *Server) renderAdminFees(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset, err := adminLimitOffset(req.Query, 100)
	if err != nil {
		return nil, err
	}
	activeOnly := adminBool(req.Query, "active_only")
	filter := walletstore.FeeConfigFilter{
		TenantID:        tenantID,
		TransactionType: adminQuery(req, "transaction_type"),
		Currency:        adminQuery(req, "currency"),
		ActiveOnly:      activeOnly,
		Limit:           limit,
		Offset:          offset,
	}
	cfgs, err := s.Service.Store.ListFeeConfigs(ctx, filter)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.FeesPage(wallethandler.FeeConfigView{
		TenantID: tenantID,
		Configs:  cfgs,
		Filter: wallethandler.FeeConfigFilterView{
			TransactionType: filter.TransactionType,
			Currency:        filter.Currency,
			ActiveOnly:      filter.ActiveOnly,
			Limit:           limit,
			Offset:          offset,
		},
		Form: wallethandler.FeeConfigFormValues{
			IsActive: true,
		},
	}))
}

func (s *Server) createAdminFee(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	form := req.Form
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	txType := adminForm(form, "transaction_type")
	if txType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTransactionType.Error())
	}
	currency, err := adminCurrency(adminForm(form, "currency"))
	if err != nil {
		return nil, mapError(err)
	}
	tierMin, err := adminNonNegativeInt64(form, "tier_min", walletstore.ErrInvalidAmount)
	if err != nil {
		return nil, err
	}
	tierMax, err := adminOptionalNonNegativeInt64(form, "tier_max")
	if err != nil {
		return nil, err
	}
	percentageFee, err := decimal.NewFromString(adminForm(form, "percentage_fee"))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidPercentage.Error())
	}
	flatFee, err := adminOptionalNonNegativeInt64Value(form, "flat_fee")
	if err != nil {
		return nil, err
	}
	minFee, err := adminOptionalNonNegativeInt64Value(form, "min_fee")
	if err != nil {
		return nil, err
	}
	maxFee, err := adminOptionalNonNegativeInt64(form, "max_fee")
	if err != nil {
		return nil, err
	}
	feeAccount := adminForm(form, "fee_account_code")
	_, err = s.Service.Store.CreateFeeConfig(ctx, walletstore.FeeConfig{
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
		IsActive:        adminBool(form, "is_active"),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return adminRedirect("/admin/wallet/fees", tenantID), nil
}

func (s *Server) renderAdminRates(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset, err := adminLimitOffset(req.Query, 100)
	if err != nil {
		return nil, err
	}
	filter := walletstore.ExchangeRateFilter{
		TenantID:      tenantID,
		BaseCurrency:  adminQuery(req, "base_currency"),
		QuoteCurrency: adminQuery(req, "quote_currency"),
		ActiveOnly:    adminBool(req.Query, "active_only"),
		Limit:         limit,
		Offset:        offset,
	}
	rates, err := s.Service.Store.ListExchangeRates(ctx, filter)
	if err != nil {
		return nil, mapError(err)
	}
	return adminHTML(ctx, wallethandler.RatesPage(wallethandler.RateView{
		TenantID: tenantID,
		Rates:    rates,
		Filter: wallethandler.RateFilterView{
			BaseCurrency:  filter.BaseCurrency,
			QuoteCurrency: filter.QuoteCurrency,
			ActiveOnly:    filter.ActiveOnly,
			Limit:         limit,
			Offset:        offset,
		},
	}))
}

func (s *Server) createAdminRate(ctx context.Context, req *walletv1.AdminWalletRequest) (*walletv1.AdminWalletResponse, error) {
	form := req.Form
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	base := adminForm(form, "base_currency")
	if base == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingBaseCurrency.Error())
	}
	quote := adminForm(form, "quote_currency")
	if quote == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingQuoteCurrency.Error())
	}
	buyRate, err := decimal.NewFromString(adminForm(form, "buy_rate"))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidRate.Error())
	}
	sellRate, err := decimal.NewFromString(adminForm(form, "sell_rate"))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidRate.Error())
	}
	spread := decimal.NullDecimal{}
	if raw := adminForm(form, "spread"); raw != "" {
		value, err := decimal.NewFromString(raw)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid spread")
		}
		spread = decimal.NullDecimal{Decimal: value, Valid: true}
	}
	setBy := adminForm(form, "set_by")
	if setBy == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingSetBy.Error())
	}
	effectiveFrom := time.Now().UTC()
	if raw := adminForm(form, "effective_from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid effective_from")
		}
		effectiveFrom = parsed
	}
	_, err = s.Service.Store.CreateExchangeRate(ctx, walletstore.ExchangeRate{
		TenantID:      tenantID,
		BaseCurrency:  base,
		QuoteCurrency: quote,
		BuyRate:       buyRate,
		SellRate:      sellRate,
		Spread:        spread,
		SetBy:         setBy,
		EffectiveFrom: effectiveFrom,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return adminRedirect("/admin/wallet/rates", tenantID), nil
}

func (s *Server) signalAdminDecision(ctx context.Context, req *walletv1.AdminWalletRequest, approved bool) (*walletv1.AdminWalletResponse, error) {
	form := req.Form
	workflowID := strings.TrimSpace(adminPath(req, "workflow_id"))
	if workflowID == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWorkflowID.Error())
	}
	kind := adminForm(form, "kind")
	if kind == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDecision.Error())
	}
	approverID, err := adminPositiveInt64(form, "approver_id", walletstore.ErrMissingApproverID)
	if err != nil {
		return nil, err
	}
	proof := adminForm(form, "proof_of_payment")
	if approved && proof == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingProofOfPayment.Error())
	}
	reason := adminForm(form, "reason")
	if !approved && reason == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingReason.Error())
	}
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	switch kind {
	case "manual_transfer":
		_, err = s.SignalManualTransferDecision(ctx, &walletv1.ManualTransferDecisionRequest{
			WorkflowId:     workflowID,
			Approved:       approved,
			ApproverId:     approverID,
			Reason:         reason,
			ProofOfPayment: proof,
		})
	case "withdrawal":
		_, err = s.SignalWithdrawalApproval(ctx, &walletv1.WithdrawalApprovalRequest{
			WorkflowId:     workflowID,
			Approved:       approved,
			ApproverId:     approverID,
			Reason:         reason,
			ProofOfPayment: proof,
		})
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported decision kind")
	}
	if err != nil {
		return nil, err
	}
	return adminRedirect("/admin/wallet/pending", tenantID), nil
}

func adminHTML(ctx context.Context, component templ.Component) (*walletv1.AdminWalletResponse, error) {
	var buf bytes.Buffer
	if err := component.Render(ctx, &buf); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &walletv1.AdminWalletResponse{
		StatusCode:  http.StatusOK,
		ContentType: adminHTMLContentType,
		Body:        buf.Bytes(),
	}, nil
}

func adminRedirect(path, tenantID string) *walletv1.AdminWalletResponse {
	path += "?tenant_id=" + url.QueryEscape(tenantID)
	return &walletv1.AdminWalletResponse{
		StatusCode:       http.StatusSeeOther,
		RedirectLocation: path,
	}
}

func adminTenantIDFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.InvalidArgument, walletstore.ErrMissingTenantID.Error())
	}
	tenantID, err := singleGatewayMetadataValue(md, gateway.GatewayTenantIDHeader)
	if err != nil {
		return "", err
	}
	tenantID, err = adminTenantID(tenantID)
	if err != nil {
		return "", mapError(err)
	}
	return tenantID, nil
}

func adminTenantID(tenantID string) (string, error) {
	return walletstore.ValidateTenantID(tenantID)
}

func adminCurrency(currency string) (string, error) {
	currency = strings.TrimSpace(currency)
	if currency == "" {
		return "", walletstore.ErrMissingCurrency
	}
	return currency, nil
}

func adminQuery(req *walletv1.AdminWalletRequest, key string) string {
	return strings.TrimSpace(req.GetQuery()[key])
}

func adminPath(req *walletv1.AdminWalletRequest, key string) string {
	return strings.TrimSpace(req.GetPath()[key])
}

func adminForm(form map[string]string, key string) string {
	return strings.TrimSpace(form[key])
}

func adminBool(values map[string]string, key string) bool {
	value := strings.TrimSpace(values[key])
	return value == "on" || value == "true"
}

func adminLimitOffset(values map[string]string, defaultLimit int) (int, int, error) {
	limit := defaultLimit
	offset := 0
	if raw := strings.TrimSpace(values["limit"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidLimit.Error())
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(values["offset"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidOffset.Error())
		}
		offset = parsed
	}
	return limit, offset, nil
}

func adminTimeRange(values map[string]string) (time.Time, time.Time, string, string, error) {
	startStr := strings.TrimSpace(values["start"])
	endStr := strings.TrimSpace(values["end"])
	if startStr == "" && endStr == "" {
		return time.Time{}, time.Time{}, "", "", nil
	}
	if startStr == "" {
		return time.Time{}, time.Time{}, startStr, endStr, status.Error(codes.InvalidArgument, walletstore.ErrMissingStartTime.Error())
	}
	if endStr == "" {
		return time.Time{}, time.Time{}, startStr, endStr, status.Error(codes.InvalidArgument, walletstore.ErrMissingEndTime.Error())
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return time.Time{}, time.Time{}, startStr, endStr, status.Error(codes.InvalidArgument, "invalid start time")
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		return time.Time{}, time.Time{}, startStr, endStr, status.Error(codes.InvalidArgument, "invalid end time")
	}
	return start, end, startStr, endStr, nil
}

func adminPositiveInt64(values map[string]string, key string, typedErr error) (int64, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return 0, status.Error(codes.InvalidArgument, typedErr.Error())
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, status.Error(codes.InvalidArgument, typedErr.Error())
	}
	return value, nil
}

func adminNonNegativeInt64(values map[string]string, key string, typedErr error) (int64, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return 0, status.Error(codes.InvalidArgument, typedErr.Error())
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, status.Error(codes.InvalidArgument, typedErr.Error())
	}
	return value, nil
}

func adminOptionalNonNegativeInt64(values map[string]string, key string) (sql.NullInt64, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return sql.NullInt64{}, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return sql.NullInt64{}, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	return sql.NullInt64{Int64: value, Valid: true}, nil
}

func adminOptionalNonNegativeInt64Value(values map[string]string, key string) (int64, error) {
	value, err := adminOptionalNonNegativeInt64(values, key)
	if err != nil || !value.Valid {
		return 0, err
	}
	return value.Int64, nil
}

func adminOptionalPositiveInt(values map[string]string, key string) (int, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, status.Error(codes.InvalidArgument, "invalid approval timeout")
	}
	return value, nil
}

func adminUUID(raw string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, walletstore.ErrMissingWalletID
	}
	return uuid.Parse(raw)
}

func adminWithdrawalApproval(txn walletstore.PSPTransaction) wallethandler.WithdrawalApprovalItem {
	item := wallethandler.WithdrawalApprovalItem{
		WorkflowID:  txn.WorkflowID.String,
		ClientRef:   txn.ClientReference,
		Amount:      txn.Amount,
		Currency:    txn.Currency,
		Provider:    txn.PSPProvider,
		Status:      txn.Status,
		RequestedAt: txn.CreatedAt,
	}
	if len(txn.RawRequest) > 0 {
		var payload struct {
			WalletID         string `json:"wallet_id"`
			OwnerType        string `json:"owner_type"`
			OwnerID          string `json:"owner_id"`
			DestinationID    int64  `json:"destination_id"`
			ApprovalRequired bool   `json:"approval_required"`
		}
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
