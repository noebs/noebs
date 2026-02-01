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
		ownership := walletactivity.NewOwnershipActivities(deps.Store)
		w.RegisterActivity(ownership)
		security := walletactivity.NewSecurityActivities(deps.Store)
		w.RegisterActivity(security)
		audit := walletactivity.NewAuditActivities(deps.Store)
		w.RegisterActivity(audit)
		manualTransfers := walletactivity.NewManualTransferActivities(deps.Store)
		w.RegisterActivity(manualTransfers)
		fees := walletactivity.NewFeeActivities(deps.Store)
		w.RegisterActivity(fees)
		limits := walletactivity.NewLimitActivities(deps.Store)
		w.RegisterActivity(limits)
		rates := walletactivity.NewRateActivities(deps.Store)
		w.RegisterActivity(rates)
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
