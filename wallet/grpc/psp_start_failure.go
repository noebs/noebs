package walletgrpc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errPSPWorkflowStartStatusUpdateFailed = errors.New("psp transaction workflow start failure status update failed")

type pspTransactionStatusUpdater interface {
	UpdatePSPTransactionStatus(ctx context.Context, tenantID, clientReference string, update walletstore.PSPStatusUpdate) error
}

func markPSPTransactionWorkflowStartFailed(ctx context.Context, updater pspTransactionStatusUpdater, tenantID, clientReference string, cause error) error {
	updateErr := updater.UpdatePSPTransactionStatus(ctx, tenantID, clientReference, walletstore.PSPStatusUpdate{
		Status:          "failed",
		ResponseMessage: sql.NullString{String: cause.Error(), Valid: true},
		LastErrorType:   sql.NullString{String: "workflow_start_failed", Valid: true},
		LastErrorAt:     sql.NullTime{Time: time.Now().UTC(), Valid: true},
	})
	if updateErr != nil {
		return errors.Join(cause, fmt.Errorf("%w: %w", errPSPWorkflowStartStatusUpdateFailed, updateErr))
	}
	return cause
}

func mapPSPWorkflowStartFailure(err error) error {
	if errors.Is(err, errPSPWorkflowStartStatusUpdateFailed) {
		return status.Error(codes.Internal, err.Error())
	}
	return mapTemporalError(err)
}
