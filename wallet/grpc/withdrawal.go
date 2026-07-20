package walletgrpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletpsp "github.com/adonese/noebs/wallet/psp"
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

func (s *Server) RequestWithdrawal(ctx context.Context, req *walletv1.WithdrawalRequest) (*walletv1.WorkflowRun, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
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
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
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
	approvalRequired := withdrawalApprovalRequired(s.Service.Config, req.Amount)
	approvalTimeoutSeconds := int(req.ApprovalTimeoutSeconds)
	if approvalRequired && approvalTimeoutSeconds <= 0 {
		approvalTimeoutSeconds = s.Service.Config.WalletApprovalTimeoutSeconds
	}
	if approvalRequired && approvalTimeoutSeconds <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingApprovalTimeout.Error())
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

	if err := s.validateWithdrawalRequest(ctx, req, walletID); err != nil {
		return nil, mapError(err)
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	workflowID := withdrawalWorkflowID(req.TenantId, req.ClientReference)
	idempotencyKey := textOrDefault(req.IdempotencyKey, req.ClientReference)
	metadata := metadataFromStruct(req.Metadata)
	rawRequest, err := withdrawalRawRequest(req, allowReturnToSource, approvalRequired, metadata)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	requestedTxn := walletstore.PSPTransaction{
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
	if existing, err := s.Service.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference); err == nil {
		if err := walletstore.ValidatePSPTransactionCreateReplay(existing, requestedTxn); err != nil {
			return nil, mapError(err)
		}
		existingID := existing.WorkflowID.String
		if !existing.WorkflowID.Valid {
			existingID = workflowID
		}
		return &walletv1.WorkflowRun{WorkflowId: existingID, RunId: ""}, nil
	} else if !errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
		return nil, mapError(err)
	}

	txn := requestedTxn
	if _, err := s.Service.Store.CreatePSPTransaction(ctx, txn); err != nil {
		return nil, mapError(err)
	}

	params := walletworkflow.WithdrawalParams{
		TenantID:                   req.TenantId,
		ProviderCode:               req.ProviderCode,
		WalletID:                   req.WalletId,
		OwnerType:                  req.OwnerType,
		OwnerID:                    req.OwnerId,
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
		return nil, mapPSPWorkflowStartFailure(markPSPTransactionWorkflowStartFailed(ctx, s.Service.Store, req.TenantId, req.ClientReference, err))
	}

	return &walletv1.WorkflowRun{
		WorkflowId: run.GetID(),
		RunId:      run.GetRunID(),
	}, nil
}

func (s *Server) validateWithdrawalRequest(ctx context.Context, req *walletv1.WithdrawalRequest, walletID uuid.UUID) error {
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
	if s == nil || s.Service == nil {
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
	prefix := withdrawalWorkflowIDPrefix(tenantID)
	if len(command.WorkflowID) <= len(prefix) || !strings.HasPrefix(command.WorkflowID, prefix) {
		return status.Error(codes.NotFound, "withdrawal workflow not found")
	}
	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	decision := walletworkflow.WithdrawalApprovalDecision{
		Approved:            command.Approved,
		DecidedByOperatorID: command.OperatorID,
		Reason:              command.Reason,
		ProofOfPayment:      command.ProofOfPayment,
	}
	return mapTemporalError(temporalClient.SignalWorkflow(
		ctx, command.WorkflowID, "", walletworkflow.WithdrawalApprovalSignal, decision,
	))
}

func withdrawalApprovalRequired(cfg ebs_fields.NoebsConfig, amount int64) bool {
	return cfg.WalletApprovalThreshold > 0 && amount >= cfg.WalletApprovalThreshold
}

func withdrawalWorkflowID(tenantID, clientReference string) string {
	return withdrawalWorkflowIDPrefix(tenantID) + clientReference
}

func withdrawalWorkflowIDPrefix(tenantID string) string {
	return fmt.Sprintf("wallet-withdrawal-%s-", tenantID)
}

func withdrawalRawRequest(req *walletv1.WithdrawalRequest, allowReturnToSource, approvalRequired bool, metadata map[string]any) (json.RawMessage, error) {
	payload := map[string]any{
		"tenant_id":              req.TenantId,
		"client_reference":       req.ClientReference,
		"provider_code":          req.ProviderCode,
		"wallet_id":              req.WalletId,
		"amount":                 req.Amount,
		"currency":               req.Currency,
		"owner_type":             req.OwnerType,
		"owner_id":               req.OwnerId,
		"destination_id":         req.DestinationId,
		"allow_return_to_source": allowReturnToSource,
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
