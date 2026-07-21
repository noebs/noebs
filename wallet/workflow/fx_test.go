package workflow

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

func TestFXReferenceSyncCanonicalizesSchedulingAndActivityPolicies(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FXReferenceSync)

	var syncedMu sync.Mutex
	synced := make(map[string]int)
	retrievedAt := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	env.RegisterActivityWithOptions(
		func(context.Context) ([]string, error) {
			return []string{"z-source", "a-source"}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityListEnabledFXSources)},
	)
	env.RegisterActivityWithOptions(
		func(_ context.Context, sourceCode string) (walletactivity.FXSyncResult, error) {
			syncedMu.Lock()
			synced[sourceCode]++
			syncedMu.Unlock()
			observationCount := 1
			if sourceCode == "z-source" {
				observationCount = 2
			}
			return walletactivity.FXSyncResult{
				SourceCode:       sourceCode,
				ObservationCount: observationCount,
				RetrievedAt:      retrievedAt,
			}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivitySyncFXSource)},
	)

	activityInfo := make(map[string]activity.Info)
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		activityInfo[info.ActivityType.Name] = *info
	})
	env.ExecuteWorkflow(FXReferenceSync)
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("workflow error = %v", env.GetWorkflowError())
	}
	syncedMu.Lock()
	if synced["a-source"] != 1 || synced["z-source"] != 1 || len(synced) != 2 {
		t.Fatalf("sync calls = %v", synced)
	}
	syncedMu.Unlock()

	var result FXReferenceSyncResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) != 2 ||
		result.Sources[0].SourceCode != "a-source" || result.Sources[0].ObservationCount != 1 ||
		result.Sources[1].SourceCode != "z-source" || result.Sources[1].ObservationCount != 2 {
		t.Fatalf("result = %+v", result)
	}

	assertFXScheduledTimeout(t, activityInfo[string(walletactivity.ActivityListEnabledFXSources)], 30*time.Second)
	assertFXScheduledTimeout(t, activityInfo[string(walletactivity.ActivitySyncFXSource)], 2*time.Minute)
	assertFXActivityOptions(t, fxListActivityOptions(), 30*time.Second, time.Second, 10*time.Second)
	assertFXActivityOptions(t, fxSyncActivityOptions(), 2*time.Minute, 2*time.Second, time.Minute)
}

func TestFXReferenceSyncLaunchesSourcesConcurrentlyAndFailsFast(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetTestTimeout(3 * time.Second)
	env.RegisterWorkflow(FXReferenceSync)
	env.RegisterActivityWithOptions(
		func(context.Context) ([]string, error) {
			return []string{"z-failure", "a-slow"}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityListEnabledFXSources)},
	)

	var slowStarted atomic.Bool
	var failureStarted atomic.Bool
	slowReady := make(chan struct{})
	env.RegisterActivityWithOptions(
		func(ctx context.Context, sourceCode string) (walletactivity.FXSyncResult, error) {
			switch sourceCode {
			case "a-slow":
				slowStarted.Store(true)
				close(slowReady)
				<-ctx.Done()
				return walletactivity.FXSyncResult{SourceCode: sourceCode}, nil
			case "z-failure":
				select {
				case <-slowReady:
				case <-ctx.Done():
					return walletactivity.FXSyncResult{}, ctx.Err()
				}
				failureStarted.Store(true)
				return walletactivity.FXSyncResult{}, temporal.NewNonRetryableApplicationError("provider failed", "test_failure", nil)
			default:
				return walletactivity.FXSyncResult{}, temporal.NewNonRetryableApplicationError("unexpected source", "test_failure", nil)
			}
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivitySyncFXSource)},
	)

	env.ExecuteWorkflow(FXReferenceSync)
	workflowErr := env.GetWorkflowError()
	if workflowErr == nil || !strings.Contains(workflowErr.Error(), "provider failed") {
		t.Fatalf("workflow error = %v", workflowErr)
	}
	if !slowStarted.Load() || !failureStarted.Load() {
		t.Fatalf("activities were not launched concurrently: slow=%v failure=%v", slowStarted.Load(), failureStarted.Load())
	}
}

func TestFXReferenceSyncRejectsNonCanonicalCatalogEntriesBeforeSync(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		codes []string
	}{
		{name: "empty", codes: []string{"ecb-reference", ""}},
		{name: "duplicate", codes: []string{"ecb-reference", "ecb-reference"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(FXReferenceSync)
			env.RegisterActivityWithOptions(
				func(context.Context) ([]string, error) { return testCase.codes, nil },
				activity.RegisterOptions{Name: string(walletactivity.ActivityListEnabledFXSources)},
			)
			syncCalls := 0
			env.RegisterActivityWithOptions(
				func(context.Context, string) (walletactivity.FXSyncResult, error) {
					syncCalls++
					return walletactivity.FXSyncResult{}, nil
				},
				activity.RegisterOptions{Name: string(walletactivity.ActivitySyncFXSource)},
			)

			env.ExecuteWorkflow(FXReferenceSync)
			workflowErr := env.GetWorkflowError()
			if workflowErr == nil || !strings.Contains(workflowErr.Error(), "invalid FX source catalog") {
				t.Fatalf("workflow error = %v", workflowErr)
			}
			if syncCalls != 0 {
				t.Fatalf("sync calls = %d", syncCalls)
			}
		})
	}
}

func assertFXScheduledTimeout(t *testing.T, info activity.Info, startToClose time.Duration) {
	t.Helper()
	if info.ActivityType.Name == "" {
		t.Fatal("activity was not scheduled")
	}
	if info.StartToCloseTimeout != startToClose {
		t.Fatalf("%s start-to-close = %s", info.ActivityType.Name, info.StartToCloseTimeout)
	}
}

func assertFXActivityOptions(t *testing.T, options temporalworkflow.ActivityOptions, startToClose, initialInterval, maximumInterval time.Duration) {
	t.Helper()
	if options.StartToCloseTimeout != startToClose {
		t.Fatalf("start-to-close = %s", options.StartToCloseTimeout)
	}
	if options.RetryPolicy == nil {
		t.Fatal("missing retry policy")
	}
	if options.RetryPolicy.InitialInterval != initialInterval ||
		options.RetryPolicy.BackoffCoefficient != 2 ||
		options.RetryPolicy.MaximumInterval != maximumInterval ||
		options.RetryPolicy.MaximumAttempts != 5 {
		t.Fatalf("retry policy = %+v", options.RetryPolicy)
	}
}
