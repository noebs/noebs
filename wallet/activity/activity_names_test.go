package activity

import "testing"

func TestActivityNamesAreNonEmptySDKStrings(t *testing.T) {
	names := []any{
		ActivityExecuteDoubleEntry,
		ActivityExecuteHeldDoubleEntry,
		ActivityExecuteSystemDebitDoubleEntry,
		ActivityExecuteMultiLegSettlement,
		ActivityExecuteSystemFundedMultiLegSettlement,
		ActivityExecuteHeldWithdrawalSettlement,
		ActivityValidateDoubleEntry,
		ActivityValidateHeldDoubleEntry,
		ActivityValidateSystemDebitDoubleEntry,
		ActivityValidateMultiLegSettlement,
		ActivityValidateHeldWithdrawalSettlement,
		ActivityCreateHold,
		ActivityValidateHold,
		ActivityReleaseHold,
		ActivityCommitHold,
		ActivityExpireHolds,
		ActivityValidateReleaseHold,
		ActivityLedgerTransactionExists,
		ActivityLedgerTransactionExistsByReference,
		ActivityGetDepositIntentByReference,
		ActivityCreateDeposit,
		ActivitySendPayout,
		ActivityGetTransactionStatus,
		ActivityEnsureSystemWallet,
		ActivityRecordFundingSource,
		ActivityLinkLedgerToFundingSource,
		ActivityLinkLedgerToWithdrawalDestination,
		ActivityResolveWithdrawalDestination,
		ActivityResolveFundingSource,
		ActivityGetReturnToSourceOptions,
		ActivityReserveFundingSourceWithdrawal,
		ActivityReleaseFundingSourceWithdrawal,
		ActivityRecordAuditEvent,
		ActivityGetP2PCommand,
		ActivityAddManualTransferApproval,
		ActivityGetManualTransferByWorkflow,
		ActivityUpdateManualTransferStatus,
		ActivityLookupWorkflowDecision,
		ActivityCloseWorkflowDecisionWindow,
		ActivityCalculateFee,
		ActivityReserveLimitUsage,
		ActivityReleaseLimitUsage,
		ActivityConsumeLimitUsage,
		ActivityConvertCurrency,
		ActivityValidateP2PTransfer,
		ActivityValidateDeposit,
		ActivityValidateWithdrawal,
		ActivityResolvePSPDepositAmounts,
		ActivityGetPSPTransactionByReference,
		ActivityAddPSPTransactionAmounts,
		ActivityListPSPTransactionsForPolling,
		ActivityListPSPTransactionsByStatus,
		ActivityUpdatePSPTransactionStatus,
		ActivityTryAcquirePSPTransactionLock,
		ActivityAcknowledgePSPWorkflowSignal,
	}
	for i, value := range names {
		name, ok := value.(string)
		if !ok {
			t.Fatalf("activity name %d has dynamic type %T; Temporal requires string", i, value)
		}
		if name == "" {
			t.Fatalf("activity name %d is empty", i)
		}
	}
}
