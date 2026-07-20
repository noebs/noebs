package activity

import "testing"

func TestActivityNamesAreNonEmptySDKStrings(t *testing.T) {
	names := []any{
		ActivityExecuteDoubleEntry,
		ActivityExecuteHeldDoubleEntry,
		ActivityExecuteSystemDebitDoubleEntry,
		ActivityValidateDoubleEntry,
		ActivityValidateHeldDoubleEntry,
		ActivityValidateSystemDebitDoubleEntry,
		ActivityCreateHold,
		ActivityValidateHold,
		ActivityReleaseHold,
		ActivityValidateReleaseHold,
		ActivityLedgerTransactionExists,
		ActivityLedgerTransactionExistsByReference,
		ActivityVerifyDeposit,
		ActivitySendPayout,
		ActivityGetTransactionStatus,
		ActivityEnsureSystemWallet,
		ActivityRecordFundingSource,
		ActivityLinkLedgerToFundingSource,
		ActivityLinkLedgerToWithdrawalDestination,
		ActivityResolveWithdrawalDestination,
		ActivityResolveFundingSource,
		ActivityGetReturnToSourceOptions,
		ActivityInitiateOwnershipVerification,
		ActivityGetOwnershipVerification,
		ActivityUpdateDestinationOwnership,
		ActivityUpdateOwnershipVerificationStatus,
		ActivityRecordAuditEvent,
		ActivityCreateManualTransfer,
		ActivityAddManualTransferApproval,
		ActivityGetManualTransferByWorkflow,
		ActivityUpdateManualTransferStatus,
		ActivityCalculateFee,
		ActivityCheckLimits,
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
