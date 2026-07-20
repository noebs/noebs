package walletgrpc

import (
	"context"
	"fmt"

	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type manualTransferDecisionCommand struct {
	WorkflowID     string
	Approved       bool
	OperatorID     int64
	Reason         string
	ProofOfPayment string
}

func (s *Server) signalManualTransferDecision(ctx context.Context, command manualTransferDecisionCommand) error {
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
		return status.Error(codes.InvalidArgument, walletstore.ErrMissingReason.Error())
	}

	tenantID, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return err
	}
	transfer, err := s.Service.Store.GetManualTransferByWorkflow(ctx, tenantID, command.WorkflowID)
	if err != nil {
		return mapError(err)
	}
	if transfer.RequestedByOperatorID == command.OperatorID {
		return status.Error(codes.InvalidArgument, walletstore.ErrApproverIsRequester.Error())
	}

	temporalClient, err := s.ensureTemporalClient()
	if err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	decision := walletworkflow.ManualTransferDecision{
		Approved:            command.Approved,
		DecidedByOperatorID: command.OperatorID,
		Reason:              command.Reason,
		ProofOfPayment:      command.ProofOfPayment,
	}
	return mapTemporalError(temporalClient.SignalWorkflow(
		ctx,
		command.WorkflowID,
		"",
		walletworkflow.ManualTransferDecisionSignal,
		decision,
	))
}

func manualTransferWorkflowID(tenantID, idempotencyKey string) string {
	return fmt.Sprintf("wallet-manual-%s-%s", tenantID, idempotencyKey)
}
