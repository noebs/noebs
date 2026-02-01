package walletgrpc

import (
	"context"
	"fmt"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
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
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTenantID.Error())
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
	if req.FromOwnerType == "" || req.ToOwnerType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerType.Error())
	}
	if req.FromOwnerId == "" || req.ToOwnerId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerID.Error())
	}
	if _, err := uuid.Parse(req.FromWalletId); err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if _, err := uuid.Parse(req.ToWalletId); err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
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

	taskQueue := s.TemporalOptions.TaskQueue
	if taskQueue == "" {
		taskQueue = walletworker.TaskQueueMain
	}

	workflowID := p2pWorkflowID(req.TenantId, idempotencyKey)
	params := walletworkflow.P2PParams{
		TenantID:       req.TenantId,
		IdempotencyKey: idempotencyKey,
		Currency:       req.Currency,
		FromWalletID:   req.FromWalletId,
		ToWalletID:     req.ToWalletId,
		Amount:         req.Amount,
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
	if tenantID == "" {
		return fmt.Sprintf("wallet-p2p-%s", idempotencyKey)
	}
	return fmt.Sprintf("wallet-p2p-%s-%s", tenantID, idempotencyKey)
}
