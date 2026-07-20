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

func (s *Server) ListFundingSources(ctx context.Context, req *walletv1.ListFundingSourcesRequest) (*walletv1.ListFundingSourcesResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	claims, err := s.claimsForRPC(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	if err := s.authorizeWalletForClaims(ctx, tenantID, walletID, claims); err != nil {
		return nil, err
	}

	sources, err := s.Service.Store.ListFundingSources(ctx, tenantID, walletID)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &walletv1.ListFundingSourcesResponse{Sources: make([]*walletv1.FundingSource, 0, len(sources))}
	for i := range sources {
		proto, err := toFundingSourceProto(&sources[i])
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		resp.Sources = append(resp.Sources, proto)
	}
	return resp, nil
}

func toFundingSourceProto(source *walletstore.FundingSource) (*walletv1.FundingSource, error) {
	if source == nil {
		return nil, nil
	}
	sourceDetails, err := structFromJSON(source.SourceDetails)
	if err != nil {
		return nil, err
	}
	withdrawalMethod, err := structFromJSON(source.WithdrawalMethod)
	if err != nil {
		return nil, err
	}
	return &walletv1.FundingSource{
		Id:                 source.ID,
		TenantId:           source.TenantID,
		WalletId:           source.WalletID.String(),
		SourceType:         source.SourceType,
		PspProvider:        nullStringValue(source.PSPProvider),
		ExternalReference:  nullStringValue(source.ExternalReference),
		VerificationStatus: source.VerificationStatus,
		Currency:           source.Currency,
		SourceDetails:      sourceDetails,
		TotalFunded:        source.TotalFunded,
		TotalWithdrawn:     source.TotalWithdrawn,
		SupportsWithdrawal: source.SupportsWithdrawal,
		WithdrawalMethod:   withdrawalMethod,
	}, nil
}

func (s *Server) CreateWithdrawalDestination(ctx context.Context, req *walletv1.CreateWithdrawalDestinationRequest) (*walletv1.CreateWithdrawalDestinationResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.LinkedFundingSourceId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingFundingSourceID.Error())
	}
	claims, err := s.claimsForRPC(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	source, err := s.Service.Store.GetFundingSourceByID(ctx, tenantID, req.LinkedFundingSourceId)
	if err != nil {
		return nil, mapError(err)
	}
	if err := s.authorizeWalletForClaims(ctx, tenantID, source.WalletID, claims); err != nil {
		return nil, err
	}
	if err := walletstore.ValidateFundingSourceReadyForWithdrawal(source); err != nil {
		return nil, mapError(err)
	}
	dest := walletstore.WithdrawalDestination{
		TenantID:              tenantID,
		WalletID:              source.WalletID,
		DestinationType:       source.SourceType,
		PSPProvider:           source.PSPProvider,
		DestinationDetails:    source.WithdrawalMethod,
		DisplayName:           sql.NullString{String: req.DisplayName, Valid: req.DisplayName != ""},
		Currency:              source.Currency,
		Country:               sql.NullString{String: req.Country, Valid: req.Country != ""},
		LinkedFundingSourceID: source.ID,
		IsActive:              true,
	}
	stored, err := s.Service.Store.CreateWithdrawalDestination(ctx, dest)
	if err != nil {
		return nil, mapError(err)
	}
	destination, err := toWithdrawalDestinationProto(stored)
	if err != nil {
		return nil, err
	}
	return &walletv1.CreateWithdrawalDestinationResponse{Destination: destination}, nil
}

func (s *Server) ListWithdrawalDestinations(ctx context.Context, req *walletv1.ListWithdrawalDestinationsRequest) (*walletv1.ListWithdrawalDestinationsResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	claims, err := s.claimsForRPC(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	if err := s.authorizeWalletForClaims(ctx, tenantID, walletID, claims); err != nil {
		return nil, err
	}

	dests, err := s.Service.Store.ListWithdrawalDestinations(ctx, tenantID, walletID, req.ActiveOnly)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &walletv1.ListWithdrawalDestinationsResponse{Destinations: make([]*walletv1.WithdrawalDestination, 0, len(dests))}
	for i := range dests {
		proto, err := toWithdrawalDestinationProto(&dests[i])
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		resp.Destinations = append(resp.Destinations, proto)
	}
	return resp, nil
}

func (s *Server) DeactivateWithdrawalDestination(ctx context.Context, req *walletv1.DeactivateWithdrawalDestinationRequest) (*walletv1.DeactivateWithdrawalDestinationResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.DestinationId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDestinationID.Error())
	}
	claims, err := s.claimsForRPC(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	if err := s.authorizeDestinationForClaims(ctx, tenantID, req.DestinationId, claims); err != nil {
		return nil, err
	}

	if err := s.Service.Store.DeactivateWithdrawalDestination(ctx, tenantID, req.DestinationId, time.Now().UTC()); err != nil {
		return nil, mapError(err)
	}
	return &walletv1.DeactivateWithdrawalDestinationResponse{}, nil
}

func toWithdrawalDestinationProto(dest *walletstore.WithdrawalDestination) (*walletv1.WithdrawalDestination, error) {
	if dest == nil {
		return nil, nil
	}
	details, err := structFromJSON(dest.DestinationDetails)
	if err != nil {
		return nil, err
	}
	return &walletv1.WithdrawalDestination{
		Id:                    dest.ID,
		TenantId:              dest.TenantID,
		WalletId:              dest.WalletID.String(),
		DestinationType:       dest.DestinationType,
		PspProvider:           nullStringValue(dest.PSPProvider),
		DestinationDetails:    details,
		DisplayName:           nullStringValue(dest.DisplayName),
		Currency:              dest.Currency,
		Country:               nullStringValue(dest.Country),
		LinkedFundingSourceId: dest.LinkedFundingSourceID,
		IsActive:              dest.IsActive,
		TotalWithdrawn:        dest.TotalWithdrawn,
	}, nil
}
