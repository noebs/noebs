package walletgrpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

func (s *Server) RequestDeposit(ctx context.Context, req *walletv1.DepositRequest) (*walletv1.WorkflowRun, error) {
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
	if req.PspTransactionId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingPSPTransactionID.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if _, err := uuid.Parse(req.WalletId); err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	workflowID := depositWorkflowID(req.TenantId, req.ClientReference)
	if existing, err := s.Service.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference); err == nil {
		existingID := existing.WorkflowID.String
		if !existing.WorkflowID.Valid {
			existingID = workflowID
		}
		return &walletv1.WorkflowRun{WorkflowId: existingID, RunId: ""}, nil
	} else if err != nil && !errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
		return nil, mapError(err)
	}

	metadata := metadataFromStruct(req.Metadata)
	rawRequest, err := depositRawRequest(req, metadata)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = req.ClientReference
	}

	feeAmount := sql.NullInt64{}
	if req.FeeAmount != nil {
		feeAmount = sql.NullInt64{Int64: req.GetFeeAmount(), Valid: true}
	}
	netAmount := sql.NullInt64{}
	if req.NetAmount != nil {
		netAmount = sql.NullInt64{Int64: req.GetNetAmount(), Valid: true}
	}

	txn := walletstore.PSPTransaction{
		TenantID:         req.TenantId,
		PSPProvider:      req.ProviderCode,
		PSPTransactionID: sql.NullString{String: req.PspTransactionId, Valid: true},
		IdempotencyKey:   idempotencyKey,
		ClientReference:  req.ClientReference,
		Direction:        "inbound",
		Amount:           req.Amount,
		FeeAmount:        feeAmount,
		NetAmount:        netAmount,
		Currency:         req.Currency,
		Status:           "initiated",
		WorkflowID:       sql.NullString{String: workflowID, Valid: workflowID != ""},
		RawRequest:       rawRequest,
	}
	if _, err := s.Service.Store.CreatePSPTransaction(ctx, txn); err != nil {
		if existing, getErr := s.Service.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference); getErr == nil {
			return &walletv1.WorkflowRun{WorkflowId: existing.WorkflowID.String, RunId: ""}, nil
		}
		return nil, mapError(err)
	}

	taskQueue := s.TemporalOptions.TaskQueue
	if taskQueue == "" {
		taskQueue = walletworker.TaskQueueMain
	}
	params := walletworkflow.DepositParams{
		TenantID:        req.TenantId,
		ProviderCode:    req.ProviderCode,
		ClientReference: req.ClientReference,
		WalletID:        req.WalletId,
		OwnerType:       req.OwnerType,
		OwnerID:         req.OwnerId,
	}
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             string(taskQueue),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.Deposit, params)
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

func depositWorkflowID(tenantID, clientReference string) string {
	if tenantID == "" {
		return fmt.Sprintf("wallet-deposit-%s", clientReference)
	}
	return fmt.Sprintf("wallet-deposit-%s-%s", tenantID, clientReference)
}

func depositRawRequest(req *walletv1.DepositRequest, metadata map[string]any) (json.RawMessage, error) {
	payload := map[string]any{
		"tenant_id":          req.TenantId,
		"client_reference":   req.ClientReference,
		"provider_code":      req.ProviderCode,
		"wallet_id":          req.WalletId,
		"owner_type":         req.OwnerType,
		"owner_id":           req.OwnerId,
		"psp_transaction_id": req.PspTransactionId,
		"amount":             req.Amount,
		"currency":           req.Currency,
		"fee_amount":         req.FeeAmount,
		"net_amount":         req.NetAmount,
		"metadata":           metadata,
	}
	return json.Marshal(payload)
}
