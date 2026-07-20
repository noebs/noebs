package walletgrpc

import (
	"context"
	"encoding/json"
	"errors"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) RequestP2PTransfer(ctx context.Context, req *walletv1.RequestP2PTransferRequest) (*walletv1.RequestP2PTransferResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if missingRequiredText(req.Currency) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if missingRequiredText(req.FromWalletId) || missingRequiredText(req.ToWalletId) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if req.FromWalletId == req.ToWalletId {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidWalletPair.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	idempotencyKey, referenceID, err := resolveIdempotencyAndReference(req.IdempotencyKey, req.ReferenceId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	claims, err := s.claimsForRPC(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	fromOwnerType, fromOwnerID, err := bindOwnerToClaims(req.FromOwnerType, req.FromOwnerId, claims)
	if err != nil {
		return nil, err
	}
	req.TenantId = tenantID
	req.FromOwnerType = fromOwnerType
	req.FromOwnerId = fromOwnerID
	if missingRequiredText(req.FromOwnerType) || missingRequiredText(req.ToOwnerType) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerType.Error())
	}
	if missingRequiredText(req.FromOwnerId) || missingRequiredText(req.ToOwnerId) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerID.Error())
	}
	fromWalletID, err := uuid.Parse(req.FromWalletId)
	if err != nil || fromWalletID == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	toWalletID, err := uuid.Parse(req.ToWalletId)
	if err != nil || toWalletID == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if err := s.authorizeWalletForClaims(ctx, tenantID, fromWalletID, claims); err != nil {
		return nil, err
	}

	workflowID := p2pWorkflowID(req.TenantId, idempotencyKey)
	params := walletworkflow.P2PParams{
		TenantID:       req.TenantId,
		IdempotencyKey: idempotencyKey,
	}
	commandDocument, err := json.Marshal(walletstore.P2PCommandPayload{
		Currency:      req.Currency,
		FromWalletID:  req.FromWalletId,
		ToWalletID:    req.ToWalletId,
		Amount:        req.Amount,
		Description:   req.Description,
		ReferenceID:   referenceID,
		FromOwnerType: req.FromOwnerType,
		FromOwnerID:   req.FromOwnerId,
		ToOwnerType:   req.ToOwnerType,
		ToOwnerID:     req.ToOwnerId,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	command := walletstore.P2PCommandReservation{
		TenantID:       req.TenantId,
		IdempotencyKey: idempotencyKey,
		WorkflowID:     workflowID,
		FromWalletID:   fromWalletID,
		ToWalletID:     toWalletID,
		FromOwnerType:  req.FromOwnerType,
		FromOwnerID:    req.FromOwnerId,
		ToOwnerType:    req.ToOwnerType,
		ToOwnerID:      req.ToOwnerId,
		Command:        walletstore.RawJSON(commandDocument),
	}
	newCommand := true
	if existing, err := s.Service.Store.GetP2PCommand(ctx, req.TenantId, idempotencyKey); err == nil {
		if err := walletstore.ValidateP2PCommandReplay(existing, command); err != nil {
			return nil, mapError(err)
		}
		if existing.RunID.Valid {
			return p2pWorkflowRun(existing), nil
		}
		newCommand = false
	} else if !errors.Is(err, walletstore.ErrP2PCommandNotFound) {
		return nil, mapError(err)
	}

	if newCommand {
		if validationErr := s.validateP2PTransferRequest(ctx, req, fromWalletID, toWalletID); validationErr != nil {
			existing, lookupErr := s.Service.Store.GetP2PCommand(ctx, req.TenantId, idempotencyKey)
			switch {
			case lookupErr == nil:
				if err := walletstore.ValidateP2PCommandReplay(existing, command); err != nil {
					return nil, mapError(err)
				}
				if existing.RunID.Valid {
					return p2pWorkflowRun(existing), nil
				}
			case errors.Is(lookupErr, walletstore.ErrP2PCommandNotFound):
				return nil, mapError(validationErr)
			default:
				return nil, mapError(lookupErr)
			}
		}
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	taskQueue, err := s.temporalTaskQueue()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	reserved, err := s.Service.Store.ReserveP2PCommand(ctx, command)
	if err != nil {
		return nil, mapError(err)
	}
	if reserved.RunID.Valid {
		return p2pWorkflowRun(reserved), nil
	}

	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             string(taskQueue),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.P2P, params)
	var runID string
	if err != nil {
		var already *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &already) {
			runID = already.RunId
		} else {
			return nil, mapTemporalError(err)
		}
	} else {
		runID = run.GetRunID()
	}
	recorded, err := s.Service.Store.RecordP2PCommandRun(ctx, req.TenantId, idempotencyKey, workflowID, runID)
	if err != nil {
		return nil, mapError(err)
	}
	return p2pWorkflowRun(recorded), nil
}

func (s *Server) validateP2PTransferRequest(ctx context.Context, req *walletv1.RequestP2PTransferRequest, fromWalletID, toWalletID uuid.UUID) error {
	validator := walletvalidation.Service{Store: s.Service.Store}
	_, err := validator.ValidateP2P(ctx, walletvalidation.P2PValidationRequest{
		TenantID:        req.TenantId,
		TransactionType: "p2p",
		FromWalletID:    fromWalletID,
		ToWalletID:      toWalletID,
		Currency:        req.Currency,
		Amount:          req.Amount,
		FromOwnerType:   req.FromOwnerType,
		FromOwnerID:     req.FromOwnerId,
		ToOwnerType:     req.ToOwnerType,
		ToOwnerID:       req.ToOwnerId,
	})
	return err
}

func p2pWorkflowID(tenantID, idempotencyKey string) string {
	return walletWorkflowID("p2p", tenantID, idempotencyKey)
}

func p2pWorkflowRun(command *walletstore.P2PCommand) *walletv1.RequestP2PTransferResponse {
	return &walletv1.RequestP2PTransferResponse{WorkflowId: command.WorkflowID, RunId: command.RunID.String}
}
