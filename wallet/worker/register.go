package worker

import (
	"errors"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"go.temporal.io/sdk/worker"
)

var (
	ErrMissingWorker        = errors.New("missing temporal worker")
	ErrMissingWalletStore   = errors.New("missing wallet store")
	ErrMissingPSPActivities = errors.New("missing PSP activities")
	ErrMissingFXActivities  = errors.New("missing FX activities")
)

type RegisterDeps struct {
	Store         *walletstore.Store
	PSPActivities *walletactivity.PSPActivities
	FXActivities  *walletactivity.FXActivities
}

func (d RegisterDeps) Validate() error {
	if d.Store == nil {
		return ErrMissingWalletStore
	}
	if d.PSPActivities == nil {
		return ErrMissingPSPActivities
	}
	if d.FXActivities == nil {
		return ErrMissingFXActivities
	}
	return nil
}

func RegisterWallet(w worker.Worker, deps RegisterDeps) error {
	if w == nil {
		return ErrMissingWorker
	}
	if err := deps.Validate(); err != nil {
		return err
	}
	w.RegisterActivity(walletactivity.NewLedgerActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewFundingActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewAuditActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewP2PActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewManualTransferActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewWorkflowDecisionActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewPSPTransactionActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewDepositIntentActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewFeeActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewLimitActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewRateActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewWalletActivities(deps.Store))
	w.RegisterActivity(walletactivity.NewValidationActivities(deps.Store))
	w.RegisterActivity(deps.PSPActivities)
	w.RegisterActivity(deps.FXActivities)
	w.RegisterWorkflow(walletworkflow.Deposit)
	w.RegisterWorkflow(walletworkflow.Withdrawal)
	w.RegisterWorkflow(walletworkflow.P2P)
	w.RegisterWorkflow(walletworkflow.ManualTransfer)
	w.RegisterWorkflow(walletworkflow.Reconciliation)
	w.RegisterWorkflow(walletworkflow.PSPStatusPoller)
	w.RegisterWorkflow(walletworkflow.FXReferenceSync)
	return nil
}
