package walletgrpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
)

func (s *Server) RequestDeposit(ctx context.Context, req *walletv1.RequestDepositRequest) (*walletv1.RequestDepositResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	req.ProviderCode = strings.TrimSpace(req.ProviderCode)
	req.WalletId = strings.TrimSpace(req.WalletId)
	req.Currency = strings.TrimSpace(req.Currency)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.Region = strings.TrimSpace(req.Region)
	if missingRequiredText(req.ProviderCode) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingProviderCode.Error())
	}
	if missingRequiredText(req.WalletId) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	if missingRequiredText(req.Currency) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if missingRequiredText(req.IdempotencyKey) {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingIdempotencyKey.Error())
	}
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil || walletID == uuid.Nil {
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
	ownerType, ownerID, err := bindOwnerToClaims("", "", claims)
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
	if err := s.authorizeWalletForClaims(ctx, tenantID, walletID, claims); err != nil {
		return nil, err
	}
	walletRow, err := s.Service.Store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return nil, mapError(err)
	}
	if err := validatePublicCurrencyUnitID(walletRow.CurrencyUnitID); err != nil {
		return nil, mapError(err)
	}
	if walletRow.Currency != req.Currency {
		return nil, mapError(walletstore.ErrCurrencyMismatch)
	}

	metadata := metadataFromStruct(req.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	rawRequest, err := depositRawRequest(req, ownerType, ownerID, metadata)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	taskQueue, err := s.temporalTaskQueue()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	intentReference, err := newDepositIntentReference()
	if err != nil {
		return nil, status.Error(codes.Internal, "create deposit intent reference")
	}
	requested := walletstore.DepositIntent{
		TenantID:        tenantID,
		IntentReference: intentReference,
		ProviderCode:    req.ProviderCode,
		WalletID:        walletID,
		OwnerType:       ownerType,
		OwnerID:         ownerID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		CurrencyUnitID:  walletRow.CurrencyUnitID,
		IdempotencyKey:  req.IdempotencyKey,
		WorkflowID:      depositWorkflowID(tenantID, intentReference),
		Metadata:        walletstore.RawJSON(metadataJSON),
		Region:          req.Region,
		RawRequest:      walletstore.RawJSON(rawRequest),
	}

	intent, err := s.Service.Store.GetDepositIntentByIdempotency(ctx, tenantID, req.ProviderCode, req.IdempotencyKey)
	switch {
	case err == nil:
		if err := walletstore.ValidateDepositIntentReplay(intent, requested); err != nil {
			return nil, mapError(err)
		}
	case errors.Is(err, walletstore.ErrDepositIntentNotFound):
		if err := s.validateDepositRequest(ctx, requested); err != nil {
			return nil, mapError(err)
		}
		intent, err = s.Service.Store.ReserveDepositIntent(ctx, requested)
		if err != nil {
			return nil, mapError(err)
		}
	default:
		return nil, mapError(err)
	}
	if intent.RunID.Valid {
		return &walletv1.RequestDepositResponse{WorkflowId: intent.WorkflowID, RunId: intent.RunID.String}, nil
	}

	params := walletworkflow.DepositParams{
		TenantID:        intent.TenantID,
		IntentReference: intent.IntentReference,
	}
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    intent.WorkflowID,
		TaskQueue:             string(taskQueue),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, walletworkflow.Deposit, params)
	if err != nil {
		var already *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &already) {
			runID := already.RunId
			recorded, recordErr := s.Service.Store.RecordDepositIntentRun(ctx, intent.TenantID, intent.IntentReference, intent.WorkflowID, runID)
			if recordErr != nil {
				return nil, mapError(recordErr)
			}
			return &walletv1.RequestDepositResponse{WorkflowId: recorded.WorkflowID, RunId: recorded.RunID.String}, nil
		}
		return nil, mapTemporalError(err)
	}
	recorded, err := s.Service.Store.RecordDepositIntentRun(ctx, intent.TenantID, intent.IntentReference, run.GetID(), run.GetRunID())
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.RequestDepositResponse{WorkflowId: recorded.WorkflowID, RunId: recorded.RunID.String}, nil
}

func (s *Server) validateDepositRequest(ctx context.Context, intent walletstore.DepositIntent) error {
	validator := walletvalidation.Service{Store: s.Service.Store}
	_, err := validator.ValidateDeposit(ctx, walletvalidation.DepositValidationRequest{
		TenantID:        intent.TenantID,
		TransactionType: "deposit",
		ProviderCode:    intent.ProviderCode,
		WalletID:        intent.WalletID,
		Currency:        intent.Currency,
		Amount:          intent.Amount,
		OwnerType:       intent.OwnerType,
		OwnerID:         intent.OwnerID,
		Region:          intent.Region,
	})
	return err
}

func depositWorkflowID(tenantID, clientReference string) string {
	return walletWorkflowID("deposit", tenantID, clientReference)
}

func newDepositIntentReference() (string, error) {
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return "", err
	}
	return "dep_" + base64.RawURLEncoding.EncodeToString(material), nil
}

func depositRawRequest(req *walletv1.RequestDepositRequest, ownerType, ownerID string, metadata map[string]any) (json.RawMessage, error) {
	payload := map[string]any{
		"tenant_id":       req.TenantId,
		"provider_code":   req.ProviderCode,
		"wallet_id":       req.WalletId,
		"owner_type":      ownerType,
		"owner_id":        ownerID,
		"amount":          req.Amount,
		"currency":        req.Currency,
		"idempotency_key": req.IdempotencyKey,
		"region":          req.Region,
		"metadata":        metadata,
	}
	return json.Marshal(payload)
}
