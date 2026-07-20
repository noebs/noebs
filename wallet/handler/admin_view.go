package handler

import (
	"context"
	"database/sql"
	"net/url"
	"strconv"
	"strings"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

type adminCSRFContextKey struct{}

func WithAdminCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, adminCSRFContextKey{}, token)
}

func backofficeCSRF(ctx context.Context) string {
	token, _ := ctx.Value(adminCSRFContextKey{}).(string)
	return token
}

type WalletDashboardView struct {
	TenantID string
}

type WalletListView struct {
	TenantID string
	Wallets  []walletstore.Wallet
}

type WalletDetailView struct {
	TenantID       string
	Wallet         walletstore.Wallet
	FundingSources []walletstore.FundingSource
	Destinations   []walletstore.WithdrawalDestination
}

type PendingApprovalsView struct {
	TenantID        string
	ManualTransfers []walletstore.ManualTransfer
	Withdrawals     []WithdrawalApprovalItem
}

type WithdrawalApprovalItem struct {
	WorkflowID     string
	ClientRef      string
	WalletID       string
	OwnerType      string
	OwnerID        string
	DestinationID  int64
	Amount         int64
	Currency       string
	Provider       string
	Status         string
	RequestedAt    time.Time
	ApprovalNeeded bool
}

type AuditLogView struct {
	TenantID string
	Events   []walletstore.AuditEvent
	Filter   AuditFilterView
}

type PSPTransactionsView struct {
	TenantID     string
	Transactions []walletstore.PSPTransaction
	Filter       PSPTransactionFilterView
}

type PSPTransactionDetailView struct {
	TenantID    string
	Transaction walletstore.PSPTransaction
}

type PSPTransactionFilterView struct {
	Status    string
	Provider  string
	Direction string
	ClientRef string
	Start     string
	End       string
	Limit     int
	Offset    int
}

type ManualTransferFormView struct {
	TenantID  string
	Values    ManualTransferFormValues
	Transfers []walletstore.ManualTransfer
	Approvals []walletstore.ManualTransferApproval
	Filter    ManualTransferFilterView
}

type ManualTransferFormValues struct {
	IdempotencyKey string
	TransferType   string
	WalletID       string
	Amount         string
	Currency       string
	Reason         string
	PSPProvider    string
	PSPReference   string
	ApprovalTTL    string
}

type ManualTransferFilterView struct {
	Status       string
	TransferType string
	WalletID     string
	Start        string
	End          string
	Limit        int
	Offset       int
}

type ManualTransferDetailView struct {
	TenantID  string
	Transfer  walletstore.ManualTransfer
	Approvals []walletstore.ManualTransferApproval
}

type FeeConfigView struct {
	TenantID string
	Configs  []walletstore.FeeConfig
	Filter   FeeConfigFilterView
	Form     FeeConfigFormValues
}

type FeeConfigFilterView struct {
	TransactionType string
	Currency        string
	ActiveOnly      bool
	Limit           int
	Offset          int
}

type FeeConfigFormValues struct {
	TransactionType string
	Currency        string
	TierMin         string
	TierMax         string
	PercentageFee   string
	FlatFee         string
	MinFee          string
	MaxFee          string
	FeeAccountCode  string
	IsActive        bool
}

type RateView struct {
	TenantID string
	Rates    []walletstore.ExchangeRate
	Filter   RateFilterView
	Form     RateFormValues
}

type RateFilterView struct {
	BaseCurrency  string
	QuoteCurrency string
	ActiveOnly    bool
	Limit         int
	Offset        int
}

type RateFormValues struct {
	BaseCurrency  string
	QuoteCurrency string
	BuyRate       string
	SellRate      string
	Spread        string
	EffectiveFrom string
}

type AuditFilterView struct {
	EventType  string
	ActorType  string
	ActorID    string
	TargetType string
	TargetID   string
	Action     string
	Start      string
	End        string
	Limit      int
	Offset     int
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func formatNullString(ns sql.NullString) string {
	if !ns.Valid || ns.String == "" {
		return "-"
	}
	return ns.String
}

func formatNullInt64(ns sql.NullInt64) string {
	if !ns.Valid {
		return "-"
	}
	return strconv.FormatInt(ns.Int64, 10)
}

func formatNullTime(nt sql.NullTime) string {
	if !nt.Valid {
		return "-"
	}
	return formatTime(nt.Time)
}

func formatJSON(raw []byte) string {
	if len(raw) == 0 {
		return "-"
	}
	return strings.TrimSpace(string(raw))
}

func formatNullDecimal(nd decimal.NullDecimal) string {
	if !nd.Valid {
		return "-"
	}
	return nd.Decimal.String()
}

func adminWalletPath(tenantID, suffix string) string {
	return "/backoffice/t/" + url.PathEscape(tenantID) + "/wallet" + suffix
}

func displayTenant(tenantID string) string {
	return tenantID
}
