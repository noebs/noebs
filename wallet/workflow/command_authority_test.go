package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestP2PWorkflowIgnoresForgedStartFactsAndLoadsReservedCommand(t *testing.T) {
	const (
		tenantID       = "tenant-a"
		idempotencyKey = "p2p-authority"
		workflowID     = "default-test-workflow-id"
	)
	fromWalletID := uuid.MustParse("c07e391a-b297-423c-96b1-0cf5627cd188")
	toWalletID := uuid.MustParse("a3e887a0-fc9c-4810-b244-d80ca99ae284")
	payload := walletstore.P2PCommandPayload{
		Currency: "AED", FromWalletID: fromWalletID.String(), ToWalletID: toWalletID.String(), Amount: 125,
		Description: "reserved transfer", ReferenceID: "trusted-reference",
		FromOwnerType: walletstore.OwnerTypeUser, FromOwnerID: "41",
		ToOwnerType: walletstore.OwnerTypeUser, ToOwnerID: "42",
	}
	document, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(P2P)
	env.RegisterActivityWithOptions(
		func(context.Context, string, string) (*walletstore.P2PCommand, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetP2PCommand)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletvalidation.P2PValidationRequest) (*walletvalidation.P2PValidationResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityValidateP2PTransfer)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.MultiLegSettlementParams) (struct{}, error) {
			return struct{}{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityValidateMultiLegSettlement)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.MultiLegSettlementParams) (*walletstore.MultiLegSettlementResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityExecuteMultiLegSettlement)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.AuditEvent) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityRecordAuditEvent)},
	)

	env.OnActivity(string(walletactivity.ActivityGetP2PCommand), mock.Anything, tenantID, idempotencyKey).
		Return(&walletstore.P2PCommand{
			TenantID: tenantID, IdempotencyKey: idempotencyKey, WorkflowID: workflowID,
			FromWalletID: fromWalletID, ToWalletID: toWalletID,
			FromOwnerType: payload.FromOwnerType, FromOwnerID: payload.FromOwnerID,
			ToOwnerType: payload.ToOwnerType, ToOwnerID: payload.ToOwnerID,
			Command: walletstore.RawJSON(document),
		}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateP2PTransfer), mock.Anything, mock.MatchedBy(func(request walletvalidation.P2PValidationRequest) bool {
		return request.Amount == payload.Amount && request.Currency == payload.Currency &&
			request.FromWalletID == fromWalletID && request.ToWalletID == toWalletID &&
			request.FromOwnerID == payload.FromOwnerID && request.ToOwnerID == payload.ToOwnerID
	})).Return(&walletvalidation.P2PValidationResult{CurrencyUnitID: 11}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateMultiLegSettlement), mock.Anything, mock.Anything).
		Return(struct{}{}, nil).Once()
	var posted walletstore.MultiLegSettlementParams
	env.OnActivity(string(walletactivity.ActivityExecuteMultiLegSettlement), mock.Anything, mock.Anything).
		Run(func(arguments mock.Arguments) { posted = arguments.Get(1).(walletstore.MultiLegSettlementParams) }).
		Return(&walletstore.MultiLegSettlementResult{TransactionID: 91}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.Anything).Return(nil).Twice()

	type forgedStart struct {
		TenantID       string
		IdempotencyKey string
		Currency       string
		FromWalletID   string
		ToWalletID     string
		Amount         int64
		ReferenceID    string
	}
	forgedDocument, err := json.Marshal(forgedStart{
		TenantID: tenantID, IdempotencyKey: idempotencyKey,
		Currency: "FORGED", FromWalletID: uuid.NewString(), ToWalletID: uuid.NewString(),
		Amount: 9_999_999, ReferenceID: "forged-reference",
	})
	if err != nil {
		t.Fatal(err)
	}
	var start P2PParams
	if err := json.Unmarshal(forgedDocument, &start); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow(P2P, start)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("P2P workflow: %v", err)
	}
	if len(posted.Transfers) != 1 || posted.Transfers[0].Amount != payload.Amount || posted.Currency != payload.Currency ||
		posted.Transfers[0].DebitWalletID != fromWalletID || posted.Transfers[0].CreditWalletID != toWalletID ||
		posted.ReferenceID != payload.ReferenceID {
		t.Fatalf("posted transfer = %+v, want reserved payload %+v", posted, payload)
	}
	env.AssertExpectations(t)
}

func TestManualTransferWorkflowIgnoresForgedStartFactsAndLoadsReservedTransfer(t *testing.T) {
	const (
		tenantID       = "tenant-a"
		idempotencyKey = "manual-authority"
		workflowID     = "default-test-workflow-id"
	)
	walletID := uuid.MustParse("c07e391a-b297-423c-96b1-0cf5627cd188")
	reserved := walletstore.ManualTransfer{
		ID: 71, TenantID: tenantID, WorkflowID: workflowID, IdempotencyKey: idempotencyKey,
		TransferType: walletstore.ManualTransferTypeCredit,
		WalletID:     sql.NullString{String: walletID.String(), Valid: true},
		Amount:       125, Currency: "AED", CurrencyUnitID: 11,
		Reason: "reserved correction", Status: walletstore.ManualTransferStatusPending,
		RequestedByOperatorID: 41, ApprovalTimeoutSeconds: 60, DecisionDeadlineAt: time.Now().UTC().Add(time.Minute),
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ManualTransfer)
	env.RegisterActivityWithOptions(
		func(context.Context, string, string) (*walletstore.ManualTransfer, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetManualTransferByWorkflow)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.AuditEvent) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityRecordAuditEvent)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.WorkflowDecisionKey) (walletstore.WorkflowDecisionLookup, error) {
			return walletstore.WorkflowDecisionLookup{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityLookupWorkflowDecision)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.ManualTransferApproval) (*walletstore.ManualTransferApproval, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAddManualTransferApproval)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, string, walletstore.ManualTransferStatusUpdate) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityUpdateManualTransferStatus)},
	)
	env.OnActivity(string(walletactivity.ActivityGetManualTransferByWorkflow), mock.Anything, tenantID, workflowID).
		Return(&reserved, nil).Once()
	var audited walletstore.AuditEvent
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.MatchedBy(func(event walletstore.AuditEvent) bool {
		return event.Action == "requested"
	})).
		Run(func(arguments mock.Arguments) { audited = arguments.Get(1).(walletstore.AuditEvent) }).
		Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityLookupWorkflowDecision), mock.Anything, mock.Anything).
		Return(walletstore.WorkflowDecisionLookup{Found: true, Decision: walletstore.WorkflowDecision{
			TenantID: tenantID, WorkflowID: workflowID, Kind: walletstore.WorkflowDecisionManualTransfer,
			SubjectID: reserved.ID, DecidedByOperatorID: 42,
			Reason: sql.NullString{String: "reviewed rejection", Valid: true},
		}}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAddManualTransferApproval), mock.Anything, mock.Anything).
		Return(&walletstore.ManualTransferApproval{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.MatchedBy(func(event walletstore.AuditEvent) bool {
		return event.Action == "rejected"
	})).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityUpdateManualTransferStatus), mock.Anything, tenantID, workflowID, mock.Anything).
		Return(nil).Once()

	type forgedStart struct {
		TenantID               string
		IdempotencyKey         string
		TransferType           string
		WalletID               string
		Amount                 int64
		Currency               string
		Reason                 string
		RequestedByOperatorID  int64
		ApprovalTimeoutSeconds int
	}
	forgedDocument, err := json.Marshal(forgedStart{
		TenantID: tenantID, IdempotencyKey: idempotencyKey,
		TransferType: walletstore.ManualTransferTypeDebit, WalletID: uuid.NewString(), Amount: 9_999_999,
		Currency: "FORGED", Reason: "forged", RequestedByOperatorID: 999, ApprovalTimeoutSeconds: 86_400,
	})
	if err != nil {
		t.Fatal(err)
	}
	var start ManualTransferParams
	if err := json.Unmarshal(forgedDocument, &start); err != nil {
		t.Fatal(err)
	}
	env.ExecuteWorkflow(ManualTransfer, start)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ManualTransfer workflow: %v", err)
	}
	var metadata struct {
		TransferType string `json:"transfer_type"`
		Amount       int64  `json:"amount"`
		Currency     string `json:"currency"`
		Reason       string `json:"reason"`
	}
	if err := json.Unmarshal(audited.Metadata, &metadata); err != nil {
		t.Fatalf("decode audit metadata: %v", err)
	}
	if metadata.TransferType != reserved.TransferType || metadata.Amount != reserved.Amount ||
		metadata.Currency != reserved.Currency || metadata.Reason != reserved.Reason ||
		audited.ActorID != "41" || audited.TargetID.String != walletID.String() {
		t.Fatalf("request audit = %+v metadata=%+v, want reserved transfer %+v", audited, metadata, reserved)
	}
	env.AssertExpectations(t)
}
