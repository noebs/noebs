package walletgrpc

import (
	"context"
	"errors"
	"fmt"

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
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *Server) RequestManualTransfer(ctx context.Context, req *walletv1.ManualTransferRequest) (*walletv1.WorkflowRun, error) {
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
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingIdempotencyKey.Error())
	}
	if err := walletstore.ValidateManualTransferType(req.TransferType); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if req.Reason == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingReason.Error())
	}
	if req.RequestedBy <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
	}
	approvalTimeoutSeconds := int(req.ApprovalTimeoutSeconds)
	if approvalTimeoutSeconds <= 0 {
		approvalTimeoutSeconds = s.Service.Config.WalletManualTransferApprovalTimeoutSeconds
	}
	if approvalTimeoutSeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalTimeout.Error())
	}
	if _, err := uuid.Parse(req.WalletId); err != nil {
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

	workflowID := manualTransferWorkflowID(tenantID, req.IdempotencyKey)
	params := walletworkflow.ManualTransferParams{
		TenantID:               tenantID,
		IdempotencyKey:         req.IdempotencyKey,
		TransferType:           req.TransferType,
		WalletID:               req.WalletId,
		Amount:                 req.Amount,
		Currency:               req.Currency,
		Reason:                 req.Reason,
		RequestedBy:            req.RequestedBy,
		PSPProvider:            req.PspProvider,
		PSPReference:           req.PspReference,
		ApprovalTimeoutSeconds: approvalTimeoutSeconds,
	}
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             string(taskQueue),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.ManualTransfer, params)
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

func (s *Server) SignalManualTransferDecision(ctx context.Context, req *walletv1.ManualTransferDecisionRequest) (*emptypb.Empty, error) {
	if s == nil || s.Service == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
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
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingReason.Error())
	}

	if s.Service.Store != nil && s.Service.Store.DB != nil && s.Service.Store.DB.DB != nil {
		transfer, err := s.Service.Store.GetManualTransferByWorkflowID(ctx, req.WorkflowId)
		if err == nil && transfer.RequestedBy.Valid && transfer.RequestedBy.Int64 == req.ApproverId {
			return nil, status.Error(codes.InvalidArgument, walletstore.ErrApproverIsRequester.Error())
		}
		if err != nil && !errors.Is(err, walletstore.ErrManualTransferNotFound) {
			return nil, mapError(err)
		}
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	decision := walletworkflow.ManualTransferDecision{
		Approved:       req.Approved,
		ApproverID:     req.ApproverId,
		Reason:         req.Reason,
		ProofOfPayment: req.ProofOfPayment,
	}
	if err := temporalClient.SignalWorkflow(ctx, req.WorkflowId, "", walletworkflow.ManualTransferDecisionSignal, decision); err != nil {
		return nil, mapTemporalError(err)
	}
	return &emptypb.Empty{}, nil
}

func manualTransferWorkflowID(tenantID, idempotencyKey string) string {
	return fmt.Sprintf("wallet-manual-%s-%s", tenantID, idempotencyKey)
}
