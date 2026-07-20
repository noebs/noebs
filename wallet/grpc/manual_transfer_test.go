package walletgrpc

import (
	"testing"

	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSignalManualTransferDecisionValidatesCommand(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	tests := []struct {
		name    string
		command manualTransferDecisionCommand
		want    error
	}{
		{name: "workflow", command: manualTransferDecisionCommand{OperatorID: 1}, want: walletstore.ErrMissingWorkflowID},
		{name: "operator", command: manualTransferDecisionCommand{WorkflowID: "wf-1"}, want: walletstore.ErrMissingApproverID},
		{
			name:    "approval proof",
			command: manualTransferDecisionCommand{WorkflowID: "wf-1", OperatorID: 1, Approved: true},
			want:    walletstore.ErrMissingProofOfPayment,
		},
		{
			name:    "rejection reason",
			command: manualTransferDecisionCommand{WorkflowID: "wf-1", OperatorID: 1},
			want:    walletstore.ErrMissingReason,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := server.signalManualTransferDecision(t.Context(), tt.command)
			if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != tt.want.Error() {
				t.Fatalf("error = %v, want invalid argument %q", err, tt.want)
			}
		})
	}
}

func TestManualTransferWorkflowIDSeparatesTenants(t *testing.T) {
	if manualTransferWorkflowID("tenant-a", "request-1") == manualTransferWorkflowID("tenant-b", "request-1") {
		t.Fatal("workflow ID is not tenant scoped")
	}
}
