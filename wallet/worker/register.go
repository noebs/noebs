package worker

import (
	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"go.temporal.io/sdk/worker"
)

type RegisterDeps struct {
	Store         *walletstore.Store
	PSPActivities *walletactivity.PSPActivities
}

func RegisterWallet(w worker.Worker, deps RegisterDeps) {
	if w == nil {
		return
	}
	if deps.Store != nil {
		ledger := walletactivity.NewLedgerActivities(deps.Store)
		w.RegisterActivity(ledger)
		funding := walletactivity.NewFundingActivities(deps.Store)
		w.RegisterActivity(funding)
		audit := walletactivity.NewAuditActivities(deps.Store)
		w.RegisterActivity(audit)
		p2p := walletactivity.NewP2PActivities(deps.Store)
		w.RegisterActivity(p2p)
		manualTransfers := walletactivity.NewManualTransferActivities(deps.Store)
		w.RegisterActivity(manualTransfers)
		workflowDecisions := walletactivity.NewWorkflowDecisionActivities(deps.Store)
		w.RegisterActivity(workflowDecisions)
		pspTransactions := walletactivity.NewPSPTransactionActivities(deps.Store)
		w.RegisterActivity(pspTransactions)
		depositIntents := walletactivity.NewDepositIntentActivities(deps.Store)
		w.RegisterActivity(depositIntents)
		fees := walletactivity.NewFeeActivities(deps.Store)
		w.RegisterActivity(fees)
		limits := walletactivity.NewLimitActivities(deps.Store)
		w.RegisterActivity(limits)
		rates := walletactivity.NewRateActivities(deps.Store)
		w.RegisterActivity(rates)
		wallets := walletactivity.NewWalletActivities(deps.Store)
		w.RegisterActivity(wallets)
		validation := walletactivity.NewValidationActivities(deps.Store)
		w.RegisterActivity(validation)
	}
	if deps.PSPActivities != nil {
		w.RegisterActivity(deps.PSPActivities)
	}
	w.RegisterWorkflow(walletworkflow.Deposit)
	w.RegisterWorkflow(walletworkflow.Withdrawal)
	w.RegisterWorkflow(walletworkflow.P2P)
	w.RegisterWorkflow(walletworkflow.ManualTransfer)
	w.RegisterWorkflow(walletworkflow.Reconciliation)
	w.RegisterWorkflow(walletworkflow.PSPStatusPoller)
}
