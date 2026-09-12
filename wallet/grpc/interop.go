package walletgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet/interop"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) walletIdentity(ctx context.Context, tenant string) (string, string, error) {
	claims, err := s.requireGatewayClaims(ctx)
	if err != nil {
		return "", "", err
	}
	bound, err := bindTenantToClaims(tenant, claims)
	if err != nil {
		return "", "", err
	}
	if s.Service.Store == nil {
		return "", "", status.Error(codes.FailedPrecondition, "missing wallet store")
	}
	return bound, strconv.FormatInt(claims.UserID, 10), nil
}
func (s *Server) GetInteropCapability(ctx context.Context, r *walletv1.GetInteropCapabilityRequest) (*walletv1.InteropCapability, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, _, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	b, err := s.Service.Store.GetInteropBinding(ctx, tenant)
	if errors.Is(err, walletstore.ErrInteropDisabled) {
		return &walletv1.InteropCapability{}, nil
	}
	if err != nil {
		return nil, interopError(err)
	}
	return &walletv1.InteropCapability{Enabled: b.Enabled, FspId: b.FSPID, Currency: b.Currency, CurrencyUnitVersion: strconv.FormatInt(b.CurrencyUnitID, 10)}, nil
}
func (s *Server) CreateInteropQuote(ctx context.Context, r *walletv1.CreateInteropQuoteRequest) (*walletv1.InteropQuote, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	q, err := interop.CreateQuote(ctx, s.Service.Store, tenant, owner, r)
	if err != nil {
		return nil, interopError(err)
	}
	return interopQuoteProto(q), nil
}
func (s *Server) GetInteropQuote(ctx context.Context, r *walletv1.GetInteropQuoteRequest) (*walletv1.InteropQuote, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(r.QuoteId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid quote_id")
	}
	q, err := s.Service.Store.GetInteropQuote(ctx, tenant, id)
	if err != nil {
		return nil, interopError(err)
	}
	if q.OwnerID != owner {
		return nil, status.Error(codes.NotFound, "interop_not_found")
	}
	return interopQuoteProto(q), nil
}
func (s *Server) CloseInteropQuote(ctx context.Context, r *walletv1.GetInteropQuoteRequest) (*walletv1.CloseInteropQuoteResponse, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(r.QuoteId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid quote_id")
	}
	closed, transferID, err := s.Service.Store.CloseInteropQuote(ctx, tenant, owner, id)
	if err != nil {
		return nil, interopError(err)
	}
	response := &walletv1.CloseInteropQuoteResponse{Closed: closed}
	if transferID != uuid.Nil {
		response.TransferId = transferID.String()
	}
	return response, nil
}
func (s *Server) RequestInteropTransfer(ctx context.Context, r *walletv1.RequestInteropTransferRequest) (*walletv1.InteropTransfer, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(r.QuoteId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid quote_id")
	}
	t, err := s.Service.Store.RequestInteropTransfer(ctx, tenant, owner, id, r.IdempotencyKey)
	if err != nil {
		return nil, interopError(err)
	}
	q, err := s.Service.Store.GetInteropQuote(ctx, tenant, t.QuoteID)
	if err != nil {
		return nil, interopError(err)
	}
	return interopTransferProto(t, q), nil
}
func (s *Server) GetInteropTransfer(ctx context.Context, r *walletv1.GetInteropTransferRequest) (*walletv1.InteropTransfer, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	var id uuid.UUID
	if r.TransferId != "" {
		id, err = uuid.Parse(r.TransferId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid transfer_id")
		}
	}
	t, err := s.Service.Store.GetInteropTransfer(ctx, tenant, owner, id, r.IdempotencyKey)
	if err != nil {
		return nil, interopError(err)
	}
	q, err := s.Service.Store.GetInteropQuote(ctx, tenant, t.QuoteID)
	if err != nil {
		return nil, interopError(err)
	}
	return interopTransferProto(t, q), nil
}
func interopQuoteProto(q *walletstore.InteropQuote) *walletv1.InteropQuote {
	var intent interop.QuoteIntent
	_ = json.Unmarshal(q.Request, &intent)
	var state interop.SDKState
	_ = json.Unmarshal(q.SDKState, &state)
	r := &walletv1.InteropQuote{QuoteId: q.ID.String(), TransferId: q.TransferID.String(), Status: q.Status, WalletId: q.WalletID.String(), AmountMinor: strconv.FormatInt(q.Amount, 10), Currency: q.Currency, CurrencyUnitVersion: strconv.FormatInt(q.CurrencyUnitID, 10), Recipient: intent.To.IDValue, RecipientName: state.GetPartiesResponse.Body.Party.Name, RecipientFsp: state.To.FSPID, ErrorCode: q.ErrorCode.String}
	if q.ExpiresAt.Valid {
		r.ExpiresAt = q.ExpiresAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return r
}
func interopTransferProto(t *walletstore.InteropTransfer, q *walletstore.InteropQuote) *walletv1.InteropTransfer {
	r := &walletv1.InteropTransfer{TransferId: t.ID.String(), QuoteId: t.QuoteID.String(), Status: t.Status, HubState: t.HubState, AmountMinor: strconv.FormatInt(q.Amount, 10), Currency: q.Currency, CurrencyUnitVersion: strconv.FormatInt(q.CurrencyUnitID, 10), ErrorCode: t.ErrorCode.String, CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	r.LifecycleStatus, r.Substatus = t.LifecycleStatus, t.Substatus
	if t.LedgerTransactionID.Valid {
		r.LedgerTransactionId = strconv.FormatInt(t.LedgerTransactionID.Int64, 10)
	}
	return r
}
func interopError(err error) error {
	switch {
	case errors.Is(err, walletstore.ErrInteropNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, walletstore.ErrInteropConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, walletstore.ErrInteropInvalid), errors.Is(err, interop.ErrProtocol):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, walletstore.ErrInteropDisabled), errors.Is(err, walletstore.ErrInteropState), errors.Is(err, walletstore.ErrInteropQuoteExpired):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return mapError(err)
	}
}
