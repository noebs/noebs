package worker

import (
	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"go.temporal.io/sdk/worker"
)

func RegisterWallet(w worker.Worker, store *walletstore.Store) {
	if w == nil {
		return
	}
	if store != nil {
		ledger := walletactivity.NewLedgerActivities(store)
		w.RegisterActivity(ledger)
	}
	w.RegisterWorkflow(walletworkflow.Deposit)
	w.RegisterWorkflow(walletworkflow.Withdrawal)
	w.RegisterWorkflow(walletworkflow.P2P)
	w.RegisterWorkflow(walletworkflow.ManualTransfer)
	w.RegisterWorkflow(walletworkflow.Reconciliation)
	w.RegisterWorkflow(walletworkflow.PSPStatusPoller)
}
