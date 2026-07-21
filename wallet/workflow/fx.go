package workflow

import (
	"fmt"
	"sort"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type FXReferenceSyncResult struct {
	Sources []walletactivity.FXSyncResult
}

func FXReferenceSync(ctx workflow.Context) (FXReferenceSyncResult, error) {
	listContext := workflow.WithActivityOptions(ctx, fxListActivityOptions())
	var sourceCodes []string
	if err := workflow.ExecuteActivity(listContext, walletactivity.ActivityListEnabledFXSources).Get(ctx, &sourceCodes); err != nil {
		return FXReferenceSyncResult{}, err
	}
	sort.Strings(sourceCodes)
	for index, sourceCode := range sourceCodes {
		if sourceCode == "" || index > 0 && sourceCode == sourceCodes[index-1] {
			return FXReferenceSyncResult{}, fmt.Errorf("invalid FX source catalog")
		}
	}

	syncContext, cancelSync := workflow.WithCancel(workflow.WithActivityOptions(ctx, fxSyncActivityOptions()))
	defer cancelSync()
	result := FXReferenceSyncResult{Sources: make([]walletactivity.FXSyncResult, len(sourceCodes))}
	selector := workflow.NewSelector(ctx)
	remaining := len(sourceCodes)
	var syncErr error
	for index, sourceCode := range sourceCodes {
		index, sourceCode := index, sourceCode
		future := workflow.ExecuteActivity(syncContext, walletactivity.ActivitySyncFXSource, sourceCode)
		selector.AddFuture(future, func(completed workflow.Future) {
			remaining--
			if syncErr != nil {
				return
			}
			var synced walletactivity.FXSyncResult
			if err := completed.Get(ctx, &synced); err != nil {
				syncErr = err
				cancelSync()
				return
			}
			if synced.SourceCode != sourceCode {
				syncErr = fmt.Errorf("FX source result mismatch: expected %s, got %s", sourceCode, synced.SourceCode)
				cancelSync()
				return
			}
			result.Sources[index] = synced
		})
	}
	for remaining > 0 && syncErr == nil {
		selector.Select(ctx)
	}
	if syncErr != nil {
		return FXReferenceSyncResult{}, syncErr
	}
	return result, nil
}

func fxListActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	}
}

func fxSyncActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    5,
		},
	}
}
