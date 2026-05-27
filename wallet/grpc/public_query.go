package walletgrpc

import (
	"context"
	"database/sql"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) ListPaymentMethodsPublic(ctx context.Context, req *walletv1.ListPaymentMethodsRequest) (*walletv1.PaymentMethodList, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	claims, err := s.requireJWTClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	methods, err := s.Service.Store.ListAvailablePSPMethods(ctx, walletstore.PSPMethodFilter{
		TenantID:  tenantID,
		Direction: req.Direction,
		Currency:  req.Currency,
		Region:    req.Region,
		Amount:    req.Amount,
		Limit:     int(req.Limit),
		Offset:    int(req.Offset),
	})
	if err != nil {
		return nil, mapError(err)
	}
	resp := make([]*walletv1.PaymentMethod, 0, len(methods))
	for _, method := range methods {
		resp = append(resp, paymentMethodProto(method))
	}
	return &walletv1.PaymentMethodList{Methods: resp}, nil
}

func (s *Server) ListWalletTransactionsPublic(ctx context.Context, req *walletv1.ListWalletTransactionsRequest) (*walletv1.WalletLedgerEntryList, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	claims, err := s.requireJWTClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if err := s.authorizeWalletForClaims(ctx, tenantID, walletID, claims); err != nil {
		return nil, err
	}
	entries, err := s.Service.Store.ListWalletLedgerEntries(ctx, walletstore.WalletLedgerEntryFilter{
		TenantID:  tenantID,
		WalletID:  walletID,
		EntryType: req.EntryType,
		Limit:     int(req.Limit),
		Offset:    int(req.Offset),
	})
	if err != nil {
		return nil, mapError(err)
	}
	resp := make([]*walletv1.WalletLedgerEntry, 0, len(entries))
	for _, entry := range entries {
		resp = append(resp, walletLedgerEntryProto(entry))
	}
	return &walletv1.WalletLedgerEntryList{Transactions: resp}, nil
}

func paymentMethodProto(method walletstore.PSPPaymentMethod) *walletv1.PaymentMethod {
	return &walletv1.PaymentMethod{
		ProviderCode:       method.ProviderCode,
		ProviderName:       method.ProviderName,
		DisplayName:        method.DisplayName,
		MethodType:         method.MethodType,
		Direction:          method.Direction,
		Currencies:         method.Currencies,
		Regions:            method.Regions,
		MinAmount:          optionalInt64(method.MinAmount),
		MaxAmount:          optionalInt64(method.MaxAmount),
		InputSchemaJson:    string(method.InputSchema),
		PresentationJson:   string(method.Presentation),
		SupportsDeposit:    method.SupportsDeposit,
		SupportsWithdrawal: method.SupportsWithdraw,
	}
}

func walletLedgerEntryProto(entry walletstore.WalletLedgerEntry) *walletv1.WalletLedgerEntry {
	return &walletv1.WalletLedgerEntry{
		Id:             entry.ID,
		TenantId:       entry.TenantID,
		TransactionId:  entry.TransactionID,
		WalletId:       entry.WalletID.String(),
		EntryType:      entry.EntryType,
		Amount:         entry.Amount,
		Currency:       entry.Currency,
		BalanceAfter:   entry.BalanceAfter,
		WalletSequence: entry.WalletSequence,
		Status:         entry.Status,
		ReferenceType:  entry.ReferenceType,
		ReferenceId:    optionalString(entry.ReferenceID),
		Description:    optionalString(entry.Description),
		MetadataJson:   string(entry.Metadata),
		CreatedAt:      entry.CreatedAt.Format(time.RFC3339Nano),
	}
}

func optionalInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}
