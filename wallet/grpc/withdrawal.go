package walletgrpc

import (
	"context"
	"database/sql"
	"errors"
	"strings"

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
	"google.golang.org/protobuf/types/known/structpb"
)

func (s *Server) RequestWithdrawal(ctx context.Context, req *walletv1.RequestWithdrawalRequest) (*walletv1.RequestWithdrawalResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	req.ClientReference = strings.TrimSpace(req.ClientReference)
	req.ProviderCode = strings.TrimSpace(req.ProviderCode)
	req.WalletId = strings.TrimSpace(req.WalletId)
	req.Currency = strings.TrimSpace(req.Currency)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.Region = strings.TrimSpace(req.Region)
	if missingRequiredText(req.ClientReference) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingClientReference.Error())
	}
	if missingRequiredText(req.ProviderCode) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingProviderCode.Error())
	}
	if missingRequiredText(req.WalletId) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if missingRequiredText(req.Currency) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil || walletID == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if req.AllowReturnToSource == nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingReturnToSourcePolicy.Error())
	}
	allowReturnToSource := req.GetAllowReturnToSource()
	if req.DestinationId < 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidDestinationID.Error())
	}
	if !allowReturnToSource && req.DestinationId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingDestinationID.Error())
	}
	if req.HoldExpirySeconds < 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidHoldExpiry.Error())
	}
	if req.HoldExpirySeconds == 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingHoldExpiry.Error())
	}
	if req.ApprovalRequired == nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalPolicy.Error())
	}
	approvalRequired := req.GetApprovalRequired()
	if req.ApprovalTimeoutSeconds < 0 || (!approvalRequired && req.ApprovalTimeoutSeconds != 0) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidApprovalTimeout.Error())
	}
	if approvalRequired && req.ApprovalTimeoutSeconds == 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalTimeout.Error())
	}
	if missingRequiredText(req.IdempotencyKey) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingIdempotencyKey.Error())
	}
	claims, err := s.claimsForRPC(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	ownerType, ownerID, err := bindOwnerToClaims(req.OwnerType, req.OwnerId, claims)
	if err != nil {
		return nil, err
	}
	if ownerType == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerType.Error())
	}
	if ownerID == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingOwnerID.Error())
	}
	req.TenantId = tenantID
	req.OwnerType = ownerType
	req.OwnerId = ownerID
	if err := s.authorizeWalletForClaims(ctx, tenantID, walletID, claims); err != nil {
		return nil, err
	}

	workflowID := withdrawalWorkflowID(req.TenantId, req.ClientReference)
	idempotencyKey := req.IdempotencyKey
	metadata := metadataFromStruct(req.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	rawRequest, err := withdrawalRawRequest(req, allowReturnToSource, approvalRequired, metadata)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	requestedTxn := walletstore.PSPTransaction{
		TenantID:            req.TenantId,
		PSPProvider:         req.ProviderCode,
		IdempotencyKey:      idempotencyKey,
		ClientReference:     req.ClientReference,
		Direction:           "outbound",
		WalletID:            uuid.NullUUID{UUID: walletID, Valid: true},
		OwnerType:           sql.NullString{String: ownerType, Valid: true},
		OwnerID:             sql.NullString{String: ownerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: allowReturnToSource, Valid: true},
		Amount:              req.Amount,
		Currency:            req.Currency,
		Status:              "initiated",
		WorkflowID:          sql.NullString{String: workflowID, Valid: workflowID != ""},
		RawRequest:          walletstore.RawJSON(rawRequest),
	}
	if req.DestinationId > 0 {
		requestedTxn.WithdrawalDestinationID = sql.NullInt64{Int64: req.DestinationId, Valid: true}
	}
	if approvalRequired {
		decisionWindowSeconds := int64(req.ApprovalTimeoutSeconds)
		if int64(req.HoldExpirySeconds) < decisionWindowSeconds {
			decisionWindowSeconds = int64(req.HoldExpirySeconds)
		}
		requestedTxn.ApprovalTimeoutSeconds = sql.NullInt64{Int64: decisionWindowSeconds, Valid: true}
	}
	newTransaction := true
	if existing, err := s.Service.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference); err == nil {
		if err := walletstore.ValidatePSPTransactionCreateReplay(existing, requestedTxn); err != nil {
			return nil, mapError(err)
		}
		newTransaction = false
	} else if !errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
		return nil, mapError(err)
	}

	if newTransaction {
		if validationErr := s.validateWithdrawalRequest(ctx, req, walletID); validationErr != nil {
			existing, lookupErr := s.Service.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference)
			switch {
			case lookupErr == nil:
				if err := walletstore.ValidatePSPTransactionCreateReplay(existing, requestedTxn); err != nil {
					return nil, mapError(err)
				}
				newTransaction = false
			case errors.Is(lookupErr, walletstore.ErrPSPTransactionNotFound):
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

	if newTransaction {
		if _, err := s.Service.Store.CreatePSPTransaction(ctx, requestedTxn); err != nil {
			return nil, mapError(err)
		}
	}

	params := walletworkflow.WithdrawalParams{
		TenantID:        req.TenantId,
		ClientReference: req.ClientReference,
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
		var already *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &already) {
			return &walletv1.RequestWithdrawalResponse{WorkflowId: workflowID, RunId: already.RunId}, nil
		}
		return nil, mapTemporalError(err)
	}

	return &walletv1.RequestWithdrawalResponse{
		WorkflowId: run.GetID(),
		RunId:      run.GetRunID(),
	}, nil
}

func (s *Server) validateWithdrawalRequest(ctx context.Context, req *walletv1.RequestWithdrawalRequest, walletID uuid.UUID) error {
	validator := walletvalidation.Service{Store: s.Service.Store}
	_, err := validator.ValidateWithdrawal(ctx, walletvalidation.WithdrawalValidationRequest{
		TenantID:        req.TenantId,
		TransactionType: "withdrawal",
		ProviderCode:    req.ProviderCode,
		WalletID:        walletID,
		Currency:        req.Currency,
		Amount:          req.Amount,
		OwnerType:       req.OwnerType,
		OwnerID:         req.OwnerId,
		Region:          req.Region,
	})
	return err
}

type withdrawalApprovalCommand struct {
	WorkflowID     string
	Approved       bool
	OperatorID     int64
	Reason         string
	ProofOfPayment string
}

func (s *Server) signalWithdrawalApproval(ctx context.Context, command withdrawalApprovalCommand) error {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if missingRequiredText(command.WorkflowID) {
		return status.Error(codes.InvalidArgument, walletstore.ErrMissingWorkflowID.Error())
	}
	if command.OperatorID <= 0 {
		return status.Error(codes.InvalidArgument, walletstore.ErrMissingApproverID.Error())
	}
	if command.Approved {
		if missingRequiredText(command.ProofOfPayment) {
			return status.Error(codes.InvalidArgument, walletstore.ErrMissingProofOfPayment.Error())
		}
	} else if missingRequiredText(command.Reason) {
		return status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalReason.Error())
	}
	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return err
	}
	txn, err := s.Service.Store.GetPSPTransactionByWorkflow(ctx, tenantID, command.WorkflowID)
	if err != nil {
		return mapError(err)
	}
	if txn.Direction != "outbound" || command.WorkflowID != withdrawalWorkflowID(tenantID, txn.ClientReference) {
		return status.Error(codes.NotFound, walletstore.ErrPSPTransactionNotFound.Error())
	}
	decision := walletworkflow.WithdrawalApprovalDecision{
		Approved:            command.Approved,
		DecidedByOperatorID: command.OperatorID,
		Reason:              command.Reason,
		ProofOfPayment:      command.ProofOfPayment,
	}
	_, err = s.Service.Store.ReserveWorkflowDecision(ctx, walletstore.WorkflowDecision{
		TenantID:            tenantID,
		WorkflowID:          command.WorkflowID,
		Kind:                walletstore.WorkflowDecisionWithdrawal,
		SubjectID:           txn.ID,
		Approved:            command.Approved,
		DecidedByOperatorID: command.OperatorID,
		Reason:              sql.NullString{String: command.Reason, Valid: command.Reason != ""},
		ProofOfPayment:      sql.NullString{String: command.ProofOfPayment, Valid: command.ProofOfPayment != ""},
	})
	if err != nil {
		return mapError(err)
	}

	if temporalClient, clientErr := s.ensureTemporalClient(); clientErr == nil {
		_ = temporalClient.SignalWorkflow(
			ctx, command.WorkflowID, "", walletworkflow.WithdrawalApprovalSignal, decision,
		)
	}
	return nil
}

func withdrawalWorkflowID(tenantID, clientReference string) string {
	return walletWorkflowID("withdrawal", tenantID, clientReference)
}

func withdrawalRawRequest(req *walletv1.RequestWithdrawalRequest, allowReturnToSource, approvalRequired bool, metadata map[string]any) (walletstore.RawJSON, error) {
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil {
		return nil, walletstore.ErrInvalidWithdrawalRequest
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	return walletstore.MarshalWithdrawalRequest(walletstore.WithdrawalRequestSnapshot{
		TenantID:               req.TenantId,
		ClientReference:        req.ClientReference,
		ProviderCode:           req.ProviderCode,
		WalletID:               walletID,
		Amount:                 req.Amount,
		Currency:               req.Currency,
		OwnerType:              req.OwnerType,
		OwnerID:                req.OwnerId,
		DestinationID:          req.DestinationId,
		AllowReturnToSource:    allowReturnToSource,
		ApprovalRequired:       approvalRequired,
		HoldExpirySeconds:      int(req.HoldExpirySeconds),
		ApprovalTimeoutSeconds: int(req.ApprovalTimeoutSeconds),
		Region:                 req.Region,
		Metadata:               metadata,
	})
}

func metadataFromStruct(metadata *structpb.Struct) map[string]any {
	if metadata == nil {
		return nil
	}
	return metadata.AsMap()
}
