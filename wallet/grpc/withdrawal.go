package walletgrpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func (s *Server) RequestWithdrawal(ctx context.Context, req *walletv1.WithdrawalRequest) (*walletv1.WorkflowRun, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTenantID.Error())
	}
	if req.ClientReference == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingClientReference.Error())
	}
	if req.ProviderCode == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingProviderCode.Error())
	}
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if req.OwnerType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerType.Error())
	}
	if req.OwnerId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerID.Error())
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
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
	userID, err := bindUserIDToClaims(req.UserId, claims)
	if err != nil {
		return nil, err
	}
	ownerType, ownerID, err := bindOwnerToClaims(req.OwnerType, req.OwnerId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	req.UserId = userID
	req.OwnerType = ownerType
	req.OwnerId = ownerID
	if err := s.authorizeWalletForClaims(ctx, tenantID, walletID, claims); err != nil {
		return nil, err
	}

	allowReturnToSource := true
	if req.AllowReturnToSource != nil {
		allowReturnToSource = req.GetAllowReturnToSource()
	}
	if !allowReturnToSource && req.DestinationId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDestinationID.Error())
	}
	holdExpirySeconds := int(req.HoldExpirySeconds)
	if holdExpirySeconds <= 0 {
		holdExpirySeconds = s.Service.Config.WalletHoldExpirySeconds
	}
	if holdExpirySeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingHoldExpiry.Error())
	}

	verificationTimeoutSeconds := int(req.VerificationTimeoutSeconds)
	if req.DestinationId > 0 && verificationTimeoutSeconds <= 0 {
		verificationTimeoutSeconds = s.Service.Config.WalletVerificationTimeoutSeconds
	}
	if req.DestinationId > 0 && verificationTimeoutSeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingVerificationTimeout.Error())
	}

	requirePIN, require2FA, approvalRequired := withdrawalRequirements(s.Service.Config, req.Amount)
	if requirePIN && req.WalletPin == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletPIN.Error())
	}
	if require2FA {
		if req.UserId <= 0 {
			return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
		}
		if req.TwoFaCode == "" {
			return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTwoFACode.Error())
		}
	}
	approvalTimeoutSeconds := int(req.ApprovalTimeoutSeconds)
	if approvalRequired && approvalTimeoutSeconds <= 0 {
		approvalTimeoutSeconds = s.Service.Config.WalletApprovalTimeoutSeconds
	}
	if approvalRequired && approvalTimeoutSeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalTimeout.Error())
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	workflowID := withdrawalWorkflowID(req.TenantId, req.ClientReference)
	if existing, err := s.Service.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference); err == nil {
		existingID := existing.WorkflowID.String
		if !existing.WorkflowID.Valid {
			existingID = workflowID
		}
		return &walletv1.WorkflowRun{WorkflowId: existingID, RunId: ""}, nil
	} else if !errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
		return nil, mapError(err)
	}

	metadata := metadataFromStruct(req.Metadata)

	rawRequest, err := withdrawalRawRequest(req, allowReturnToSource, requirePIN, require2FA, approvalRequired, metadata)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = req.ClientReference
	}

	txn := walletstore.PSPTransaction{
		TenantID:        req.TenantId,
		PSPProvider:     req.ProviderCode,
		IdempotencyKey:  idempotencyKey,
		ClientReference: req.ClientReference,
		Direction:       "outbound",
		Amount:          req.Amount,
		Currency:        req.Currency,
		Status:          "initiated",
		WorkflowID:      sql.NullString{String: workflowID, Valid: workflowID != ""},
		RawRequest:      walletstore.RawJSON(rawRequest),
	}
	if _, err := s.Service.Store.CreatePSPTransaction(ctx, txn); err != nil {
		if existing, getErr := s.Service.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference); getErr == nil {
			return &walletv1.WorkflowRun{WorkflowId: existing.WorkflowID.String, RunId: ""}, nil
		}
		return nil, mapError(err)
	}

	params := walletworkflow.WithdrawalParams{
		TenantID:                   req.TenantId,
		ProviderCode:               req.ProviderCode,
		WalletID:                   req.WalletId,
		OwnerType:                  req.OwnerType,
		OwnerID:                    req.OwnerId,
		UserID:                     req.UserId,
		WalletPIN:                  req.WalletPin,
		RequirePIN:                 requirePIN,
		TwoFACode:                  req.TwoFaCode,
		Require2FA:                 require2FA,
		DestinationID:              req.DestinationId,
		AllowReturnToSource:        allowReturnToSource,
		ApprovalRequired:           approvalRequired,
		ApprovalTimeoutSeconds:     approvalTimeoutSeconds,
		VerificationTimeoutSeconds: verificationTimeoutSeconds,
		HoldExpirySeconds:          holdExpirySeconds,
		Region:                     req.Region,
		Request: walletpsp.PayoutRequest{
			ClientReference: req.ClientReference,
			Amount:          req.Amount,
			Currency:        req.Currency,
			Metadata:        metadata,
		},
	}
	taskQueue, err := s.temporalTaskQueue()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             string(taskQueue),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.Withdrawal, params)
	if err != nil {
		if already, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); ok {
			return &walletv1.WorkflowRun{WorkflowId: workflowID, RunId: already.RunId}, nil
		}
		_ = s.Service.Store.UpdatePSPTransactionStatus(ctx, req.TenantId, req.ClientReference, walletstore.PSPStatusUpdate{
			Status:          "failed",
			ResponseMessage: sql.NullString{String: err.Error(), Valid: true},
			LastErrorType:   sql.NullString{String: "workflow_start_failed", Valid: true},
			LastErrorAt:     sql.NullTime{Time: time.Now().UTC(), Valid: true},
		})
		return nil, mapTemporalError(err)
	}

	return &walletv1.WorkflowRun{
		WorkflowId: run.GetID(),
		RunId:      run.GetRunID(),
	}, nil
}

func (s *Server) SignalWithdrawalApproval(ctx context.Context, req *walletv1.WithdrawalApprovalRequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	md, _ := metadata.FromIncomingContext(ctx)
	if err := s.requireAdmin(md); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.WorkflowId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWorkflowID.Error())
	}
	if req.ApproverId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApproverID.Error())
	}
	if req.Approved {
		if req.ProofOfPayment == "" {
			return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingProofOfPayment.Error())
		}
	} else if req.Reason == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalReason.Error())
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	decision := walletworkflow.WithdrawalApprovalDecision{
		Approved:       req.Approved,
		ApproverID:     req.ApproverId,
		Reason:         req.Reason,
		ProofOfPayment: req.ProofOfPayment,
	}
	if err := temporalClient.SignalWorkflow(ctx, req.WorkflowId, "", walletworkflow.WithdrawalApprovalSignal, decision); err != nil {
		return nil, mapTemporalError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) SignalWithdrawalVerification(ctx context.Context, req *walletv1.WithdrawalDestinationVerificationRequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	md, _ := metadata.FromIncomingContext(ctx)
	if err := s.requireAdmin(md); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.WorkflowId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWorkflowID.Error())
	}
	if req.VerificationId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingVerificationID.Error())
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	decision := walletworkflow.DestinationVerificationDecision{
		VerificationID: req.VerificationId,
		Verified:       req.Verified,
		Reason:         req.Reason,
	}
	if err := temporalClient.SignalWorkflow(ctx, req.WorkflowId, "", walletworkflow.WithdrawalVerificationSignal, decision); err != nil {
		return nil, mapTemporalError(err)
	}
	return &emptypb.Empty{}, nil
}

func withdrawalRequirements(cfg ebs_fields.NoebsConfig, amount int64) (bool, bool, bool) {
	requirePIN := cfg.WalletPINRequired
	require2FA := false
	approvalRequired := false

	if cfg.Wallet2FAThreshold > 0 && amount >= cfg.Wallet2FAThreshold {
		require2FA = true
		requirePIN = true
	}
	if cfg.WalletApprovalThreshold > 0 && amount >= cfg.WalletApprovalThreshold {
		approvalRequired = true
		requirePIN = true
		require2FA = true
	}
	return requirePIN, require2FA, approvalRequired
}

func withdrawalWorkflowID(tenantID, clientReference string) string {
	if tenantID == "" {
		return fmt.Sprintf("wallet-withdrawal-%s", clientReference)
	}
	return fmt.Sprintf("wallet-withdrawal-%s-%s", tenantID, clientReference)
}

func withdrawalRawRequest(req *walletv1.WithdrawalRequest, allowReturnToSource, requirePIN, require2FA, approvalRequired bool, metadata map[string]any) (json.RawMessage, error) {
	payload := map[string]any{
		"tenant_id":              req.TenantId,
		"client_reference":       req.ClientReference,
		"provider_code":          req.ProviderCode,
		"wallet_id":              req.WalletId,
		"amount":                 req.Amount,
		"currency":               req.Currency,
		"user_id":                req.UserId,
		"owner_type":             req.OwnerType,
		"owner_id":               req.OwnerId,
		"destination_id":         req.DestinationId,
		"allow_return_to_source": allowReturnToSource,
		"require_pin":            requirePIN,
		"require_2fa":            require2FA,
		"approval_required":      approvalRequired,
		"region":                 req.Region,
		"metadata":               metadata,
	}
	return json.Marshal(payload)
}

func metadataFromStruct(metadata *structpb.Struct) map[string]any {
	if metadata == nil {
		return nil
	}
	return metadata.AsMap()
}
