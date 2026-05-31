package walletgrpc

import (
	"context"
	"fmt"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) RequestP2PTransfer(ctx context.Context, req *walletv1.P2PTransferRequest) (*walletv1.WorkflowRun, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if req.FromWalletId == "" || req.ToWalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if req.FromWalletId == req.ToWalletId {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidWalletPair.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	claims, err := s.claimsForRPC(ctx)
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
	fromOwnerType, fromOwnerID, err := bindOwnerToClaims(req.FromOwnerType, req.FromOwnerId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	req.UserId = userID
	req.FromOwnerType = fromOwnerType
	req.FromOwnerId = fromOwnerID
	if req.FromOwnerType == "" || req.ToOwnerType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerType.Error())
	}
	if req.FromOwnerId == "" || req.ToOwnerId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerID.Error())
	}
	fromWalletID, err := uuid.Parse(req.FromWalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if _, err := uuid.Parse(req.ToWalletId); err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if err := s.authorizeWalletForClaims(ctx, tenantID, fromWalletID, claims); err != nil {
		return nil, err
	}

	requirePIN, require2FA := p2pRequirements(s.Service.Config, req.Amount)
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

	idempotencyKey := req.IdempotencyKey
	referenceID := req.ReferenceId
	if idempotencyKey == "" && referenceID == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingIdempotencyKey.Error())
	}
	if idempotencyKey == "" {
		idempotencyKey = referenceID
	}
	if referenceID == "" {
		referenceID = idempotencyKey
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	taskQueue, err := s.temporalTaskQueue()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	workflowID := p2pWorkflowID(req.TenantId, idempotencyKey)
	params := walletworkflow.P2PParams{
		TenantID:       req.TenantId,
		IdempotencyKey: idempotencyKey,
		Currency:       req.Currency,
		FromWalletID:   req.FromWalletId,
		ToWalletID:     req.ToWalletId,
		Amount:         req.Amount,
		UserID:         req.UserId,
		WalletPIN:      req.WalletPin,
		RequirePIN:     requirePIN,
		TwoFACode:      req.TwoFaCode,
		Require2FA:     require2FA,
		Description:    req.Description,
		ReferenceID:    referenceID,
		FromOwnerType:  req.FromOwnerType,
		FromOwnerID:    req.FromOwnerId,
		ToOwnerType:    req.ToOwnerType,
		ToOwnerID:      req.ToOwnerId,
	}
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             string(taskQueue),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.P2P, params)
	if err != nil {
		if already, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); ok {
			return &walletv1.WorkflowRun{WorkflowId: workflowID, RunId: already.RunId}, nil
		}
		return nil, mapTemporalError(err)
	}

	return &walletv1.WorkflowRun{
		WorkflowId: run.GetID(),
		RunId:      run.GetRunID(),
	}, nil
}

func p2pWorkflowID(tenantID, idempotencyKey string) string {
	return fmt.Sprintf("wallet-p2p-%s-%s", tenantID, idempotencyKey)
}

func p2pRequirements(cfg ebs_fields.NoebsConfig, amount int64) (bool, bool) {
	requirePIN := cfg.WalletPINRequired
	require2FA := false

	if cfg.Wallet2FAThreshold > 0 && amount >= cfg.Wallet2FAThreshold {
		require2FA = true
		requirePIN = true
	}
	return requirePIN, require2FA
}
