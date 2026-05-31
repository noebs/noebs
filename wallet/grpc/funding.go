package walletgrpc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletsecurity "github.com/adonese/noebs/wallet/security"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *Server) CreateFundingSource(ctx context.Context, req *walletv1.CreateFundingSourceRequest) (*walletv1.FundingSource, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenantID, err := validateGRPCTenantID(req.TenantId)
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
	if req.SourceType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingSourceType.Error())
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if req.SourceDetails == nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingSourceDetails.Error())
	}
	verificationStatus := req.VerificationStatus
	if verificationStatus == "" {
		verificationStatus = "unverified"
	}

	sourceDetails, err := rawFromStruct(req.SourceDetails)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	withdrawalMethod, err := rawFromStruct(req.WithdrawalMethod)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	source := walletstore.FundingSource{
		TenantID:           tenantID,
		WalletID:           walletID,
		SourceType:         req.SourceType,
		PSPProvider:        sql.NullString{String: req.PspProvider, Valid: req.PspProvider != ""},
		ExternalReference:  sql.NullString{String: req.ExternalReference, Valid: req.ExternalReference != ""},
		VerificationStatus: verificationStatus,
		Currency:           req.Currency,
		SourceDetails:      sourceDetails,
		SupportsWithdrawal: req.SupportsWithdrawal,
		WithdrawalMethod:   withdrawalMethod,
		TotalFunded:        0,
		TotalWithdrawn:     0,
		LastFundedAt:       sql.NullTime{},
		LastWithdrawnAt:    sql.NullTime{},
	}

	stored, err := s.Service.Store.UpsertFundingSource(ctx, source)
	if err != nil {
		return nil, mapError(err)
	}
	return toFundingSourceProto(stored)
}

func (s *Server) ListFundingSources(ctx context.Context, req *walletv1.ListFundingSourcesRequest) (*walletv1.FundingSourceList, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
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
	claims, err := s.claimsFromContext(ctx)
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
	resp := &walletv1.FundingSourceList{Sources: make([]*walletv1.FundingSource, 0, len(sources))}
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

func (s *Server) CreateWithdrawalDestination(ctx context.Context, req *walletv1.CreateWithdrawalDestinationRequest) (*walletv1.WithdrawalDestination, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
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
	if req.DestinationType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDestinationType.Error())
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if req.DestinationDetails == nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDestinationDetails.Error())
	}
	if req.IsReturnToSource && req.LinkedFundingSourceId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingFundingSourceID.Error())
	}
	if !req.IsReturnToSource && req.OwnershipVerificationMethod == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingVerificationType.Error())
	}
	claims, err := s.claimsFromContext(ctx)
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

	details, err := rawFromStruct(req.DestinationDetails)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	ownershipStatus := walletstore.DestinationOwnershipStatusPending
	var ownershipVerifiedAt sql.NullTime
	if req.IsReturnToSource {
		source, err := s.Service.Store.GetFundingSourceByID(ctx, tenantID, req.LinkedFundingSourceId)
		if err != nil {
			return nil, mapError(err)
		}
		if source.WalletID != walletID {
			return nil, mapError(walletstore.ErrFundingSourceNotFound)
		}
		if source.Currency != req.Currency {
			return nil, mapError(walletstore.ErrCurrencyMismatch)
		}
		if err := walletstore.ValidateFundingSourceReadyForWithdrawal(source); err != nil {
			return nil, mapError(err)
		}
		ownershipStatus = walletstore.DestinationOwnershipStatusVerified
		ownershipVerifiedAt = source.VerifiedAt
	}

	dest := walletstore.WithdrawalDestination{
		TenantID:                    req.TenantId,
		WalletID:                    walletID,
		DestinationType:             req.DestinationType,
		PSPProvider:                 sql.NullString{String: req.PspProvider, Valid: req.PspProvider != ""},
		DestinationDetails:          details,
		DisplayName:                 sql.NullString{String: req.DisplayName, Valid: req.DisplayName != ""},
		Currency:                    req.Currency,
		Country:                     sql.NullString{String: req.Country, Valid: req.Country != ""},
		OwnershipStatus:             ownershipStatus,
		OwnershipVerificationMethod: sql.NullString{String: req.OwnershipVerificationMethod, Valid: req.OwnershipVerificationMethod != ""},
		OwnershipVerifiedAt:         ownershipVerifiedAt,
		LinkedFundingSourceID:       sql.NullInt64{Int64: req.LinkedFundingSourceId, Valid: req.LinkedFundingSourceId > 0},
		IsReturnToSource:            req.IsReturnToSource,
		IsActive:                    true,
		TotalWithdrawn:              0,
	}

	stored, err := s.Service.Store.CreateWithdrawalDestination(ctx, dest)
	if err != nil {
		return nil, mapError(err)
	}
	return toWithdrawalDestinationProto(stored)
}

func (s *Server) ListWithdrawalDestinations(ctx context.Context, req *walletv1.ListWithdrawalDestinationsRequest) (*walletv1.WithdrawalDestinationList, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
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
	claims, err := s.claimsFromContext(ctx)
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
	resp := &walletv1.WithdrawalDestinationList{Destinations: make([]*walletv1.WithdrawalDestination, 0, len(dests))}
	for i := range dests {
		proto, err := toWithdrawalDestinationProto(&dests[i])
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		resp.Destinations = append(resp.Destinations, proto)
	}
	return resp, nil
}

func (s *Server) DeactivateWithdrawalDestination(ctx context.Context, req *walletv1.DeactivateWithdrawalDestinationRequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.DestinationId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDestinationID.Error())
	}
	claims, err := s.claimsFromContext(ctx)
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
	return &emptypb.Empty{}, nil
}

func (s *Server) RequestOwnershipVerification(ctx context.Context, req *walletv1.RequestOwnershipVerificationRequest) (*walletv1.OwnershipVerification, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.DestinationId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDestinationID.Error())
	}
	if req.VerificationType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingVerificationType.Error())
	}
	if req.VerificationTimeoutSeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingVerificationTimeout.Error())
	}
	claims, err := s.claimsFromContext(ctx)
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

	now := time.Now().UTC()
	maxAttempts := 3
	verification := walletstore.OwnershipVerification{
		TenantID:         req.TenantId,
		DestinationID:    req.DestinationId,
		VerificationType: req.VerificationType,
		Status:           "pending",
		Attempts:         0,
		MaxAttempts:      maxAttempts,
		ExpiresAt:        now.Add(time.Duration(req.VerificationTimeoutSeconds) * time.Second),
		WorkflowID:       sql.NullString{String: req.WorkflowId, Valid: req.WorkflowId != ""},
		ReferenceID:      sql.NullString{String: req.ReferenceId, Valid: req.ReferenceId != ""},
	}
	stored, err := s.Service.Store.CreateOwnershipVerification(ctx, verification)
	if err != nil {
		return nil, mapError(err)
	}
	if err := s.Service.Store.UpdateWithdrawalDestinationOwnership(ctx, req.TenantId, req.DestinationId, "pending", sql.NullTime{}, now); err != nil {
		return nil, mapError(err)
	}
	return toOwnershipVerificationProto(stored)
}

func (s *Server) CompleteOwnershipVerification(ctx context.Context, req *walletv1.CompleteOwnershipVerificationRequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.VerificationId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingVerificationID.Error())
	}
	if !req.Verified && req.Reason == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingReason.Error())
	}
	claims, err := s.claimsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	verification, err := s.authorizeVerificationForClaims(ctx, tenantID, req.VerificationId, claims)
	if err != nil {
		return nil, err
	}

	if verification == nil {
		verification, err = s.Service.Store.GetOwnershipVerification(ctx, tenantID, req.VerificationId)
		if err != nil {
			return nil, mapError(err)
		}
	}

	now := time.Now().UTC()
	statusValue := "failed"
	if req.Verified {
		statusValue = "verified"
	}
	if err := s.Service.Store.UpdateOwnershipVerificationStatus(ctx, req.TenantId, req.VerificationId, statusValue, now); err != nil {
		return nil, mapError(err)
	}

	if req.Verified {
		if err := s.Service.Store.UpdateWithdrawalDestinationOwnership(ctx, req.TenantId, verification.DestinationID, "verified", sql.NullTime{Time: now, Valid: true}, now); err != nil {
			return nil, mapError(err)
		}
	} else {
		if err := s.Service.Store.UpdateWithdrawalDestinationOwnership(ctx, req.TenantId, verification.DestinationID, "rejected", sql.NullTime{}, now); err != nil {
			return nil, mapError(err)
		}
	}

	if verification.WorkflowID.Valid {
		client, err := s.ensureTemporalClient()
		if err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		decision := walletworkflow.DestinationVerificationDecision{
			VerificationID: req.VerificationId,
			Verified:       req.Verified,
			Reason:         req.Reason,
		}
		if err := client.SignalWorkflow(ctx, verification.WorkflowID.String, "", walletworkflow.WithdrawalVerificationSignal, decision); err != nil {
			return nil, mapTemporalError(err)
		}
	}

	return &emptypb.Empty{}, nil
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
		Id:                          dest.ID,
		TenantId:                    dest.TenantID,
		WalletId:                    dest.WalletID.String(),
		DestinationType:             dest.DestinationType,
		PspProvider:                 nullStringValue(dest.PSPProvider),
		DestinationDetails:          details,
		DisplayName:                 nullStringValue(dest.DisplayName),
		Currency:                    dest.Currency,
		Country:                     nullStringValue(dest.Country),
		OwnershipStatus:             dest.OwnershipStatus,
		OwnershipVerificationMethod: nullStringValue(dest.OwnershipVerificationMethod),
		LinkedFundingSourceId:       nullInt64Value(dest.LinkedFundingSourceID),
		IsReturnToSource:            dest.IsReturnToSource,
		IsActive:                    dest.IsActive,
		TotalWithdrawn:              dest.TotalWithdrawn,
	}, nil
}

func toOwnershipVerificationProto(verification *walletstore.OwnershipVerification) (*walletv1.OwnershipVerification, error) {
	if verification == nil {
		return nil, nil
	}
	return &walletv1.OwnershipVerification{
		Id:               verification.ID,
		TenantId:         verification.TenantID,
		DestinationId:    verification.DestinationID,
		VerificationType: verification.VerificationType,
		Status:           verification.Status,
		Attempts:         int32(verification.Attempts),
		MaxAttempts:      int32(verification.MaxAttempts),
		WorkflowId:       nullStringValue(verification.WorkflowID),
		ReferenceId:      nullStringValue(verification.ReferenceID),
	}, nil
}

func (s *Server) ResetWalletPIN(ctx context.Context, req *walletv1.ResetWalletPINRequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenantID, err := validateGRPCTenantID(req.TenantId)
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
	if req.AdminId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApproverID.Error())
	}
	if req.NewPin == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletPIN.Error())
	}

	hash, err := walletsecurity.HashPIN(req.NewPin)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.Service.Store.UpdateWalletPIN(ctx, tenantID, walletID, hash, time.Now().UTC()); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) SetWalletPIN(ctx context.Context, req *walletv1.SetWalletPINRequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
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
	if req.NewPin == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletPIN.Error())
	}
	claims, err := s.claimsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID

	walletRow, err := s.Service.Store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return nil, mapError(err)
	}
	if !walletOwnedByClaims(walletRow, claims) {
		return nil, status.Error(codes.NotFound, walletstore.ErrWalletNotFound.Error())
	}
	if walletRow.WalletPinHash.Valid {
		if req.CurrentPin == "" {
			return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletPIN.Error())
		}
		if !walletsecurity.VerifyPIN(walletRow.WalletPinHash.String, req.CurrentPin) {
			return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidWalletPIN.Error())
		}
	}

	hash, err := walletsecurity.HashPIN(req.NewPin)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.Service.Store.UpdateWalletPIN(ctx, tenantID, walletID, hash, time.Now().UTC()); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) EnrollUser2FA(ctx context.Context, req *walletv1.EnrollUser2FARequest) (*walletv1.User2FASetup, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	claims, err := s.claimsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	userID, err := bindUserIDToClaims(req.UserId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	req.UserId = userID
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Noebs",
		AccountName: tenantID + ":" + fmt.Sprint(req.UserId),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	secret := key.Secret()
	stored, err := s.Service.Store.CreateOrResetUserTwoFA(ctx, tenantID, req.UserId, secret)
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.User2FASetup{
		TenantId:   stored.TenantID,
		UserId:     stored.UserID,
		Secret:     secret,
		OtpAuthUrl: key.URL(),
		Enabled:    stored.Enabled,
	}, nil
}

func (s *Server) ConfirmUser2FA(ctx context.Context, req *walletv1.ConfirmUser2FARequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	claims, err := s.claimsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	userID, err := bindUserIDToClaims(req.UserId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	req.UserId = userID
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
	}
	if req.Code == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTwoFACode.Error())
	}

	record, err := s.Service.Store.GetUserTwoFA(ctx, tenantID, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	if !walletsecurity.VerifyTOTP(record.Secret, req.Code) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidTwoFACode.Error())
	}
	now := time.Now().UTC()
	if err := s.Service.Store.SetUserTwoFAEnabled(ctx, tenantID, req.UserId, true, now); err != nil {
		return nil, mapError(err)
	}
	if err := s.Service.Store.TouchUserTwoFALastUsed(ctx, tenantID, req.UserId, now); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) DisableUser2FA(ctx context.Context, req *walletv1.DisableUser2FARequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	claims, err := s.claimsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	userID, err := bindUserIDToClaims(req.UserId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	req.UserId = userID
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
	}
	if req.Code == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTwoFACode.Error())
	}

	record, err := s.Service.Store.GetUserTwoFA(ctx, tenantID, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	if !walletsecurity.VerifyTOTP(record.Secret, req.Code) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidTwoFACode.Error())
	}
	now := time.Now().UTC()
	if err := s.Service.Store.SetUserTwoFAEnabled(ctx, tenantID, req.UserId, false, now); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
}
