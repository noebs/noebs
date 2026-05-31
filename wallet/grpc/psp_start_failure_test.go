package walletgrpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakePSPStatusUpdater struct {
	err     error
	updates []capturedPSPStatusUpdate
}

type capturedPSPStatusUpdate struct {
	tenantID        string
	clientReference string
	update          walletstore.PSPStatusUpdate
}

func (f *fakePSPStatusUpdater) UpdatePSPTransactionStatus(ctx context.Context, tenantID, clientReference string, update walletstore.PSPStatusUpdate) error {
	f.updates = append(f.updates, capturedPSPStatusUpdate{
		tenantID:        tenantID,
		clientReference: clientReference,
		update:          update,
	})
	return f.err
}

func TestMarkPSPTransactionWorkflowStartFailedRecordsFailure(t *testing.T) {
	cause := errors.New("temporal unavailable")
	updater := &fakePSPStatusUpdater{}

	err := markPSPTransactionWorkflowStartFailed(context.Background(), updater, "tenant", "client-ref", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cause %v", err, cause)
	}
	if len(updater.updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(updater.updates))
	}
	got := updater.updates[0]
	if got.tenantID != "tenant" || got.clientReference != "client-ref" {
		t.Fatalf("update identity = %q/%q, want tenant/client-ref", got.tenantID, got.clientReference)
	}
	if got.update.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.update.Status)
	}
	if !got.update.ResponseMessage.Valid || got.update.ResponseMessage.String != cause.Error() {
		t.Fatalf("response message = %#v, want cause message", got.update.ResponseMessage)
	}
	if !got.update.LastErrorType.Valid || got.update.LastErrorType.String != "workflow_start_failed" {
		t.Fatalf("last error type = %#v, want workflow_start_failed", got.update.LastErrorType)
	}
	if !got.update.LastErrorAt.Valid || got.update.LastErrorAt.Time.IsZero() {
		t.Fatalf("last error at = %#v, want populated timestamp", got.update.LastErrorAt)
	}
}

func TestMarkPSPTransactionWorkflowStartFailedSurfacesUpdateFailure(t *testing.T) {
	cause := errors.New("temporal rejected")
	updateErr := errors.New("status write failed")
	updater := &fakePSPStatusUpdater{err: updateErr}

	err := markPSPTransactionWorkflowStartFailed(context.Background(), updater, "tenant", "client-ref", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cause %v", err, cause)
	}
	if !errors.Is(err, updateErr) {
		t.Fatalf("error = %v, want update error %v", err, updateErr)
	}
	if !errors.Is(err, errPSPWorkflowStartStatusUpdateFailed) {
		t.Fatalf("error = %v, want sentinel %v", err, errPSPWorkflowStartStatusUpdateFailed)
	}

	mapped := mapPSPWorkflowStartFailure(err)
	if status.Code(mapped) != codes.Internal {
		t.Fatalf("mapped code = %v, want %v", status.Code(mapped), codes.Internal)
	}
	msg := status.Convert(mapped).Message()
	if !strings.Contains(msg, cause.Error()) || !strings.Contains(msg, updateErr.Error()) {
		t.Fatalf("mapped message = %q, want both cause and update error", msg)
	}
}
