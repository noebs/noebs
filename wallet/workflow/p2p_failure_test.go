package workflow

import (
	"context"
	"encoding/json"
	"testing"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestP2PReviewedFeeFailurePersistsFenceWithoutPosting(t *testing.T) {
	from, to := uuid.New(), uuid.New()
	fee, unit := int64(0), int64(14)
	p := walletstore.P2PCommandPayload{Currency: "SDG", FromWalletID: from.String(), ToWalletID: to.String(), Amount: 100, ReferenceID: "ref", FromOwnerType: "user", FromOwnerID: "1", ToOwnerType: "user", ToOwnerID: "2", ExpectedFeeAmount: &fee, ExpectedCurrencyUnitVersion: &unit}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(context.Context, string, string) (*walletstore.P2PCommand, error) {
		return &walletstore.P2PCommand{TenantID: "tenant", IdempotencyKey: "key", WorkflowID: "default-test-workflow-id", FromWalletID: from, ToWalletID: to, FromOwnerType: "user", FromOwnerID: "1", ToOwnerType: "user", ToOwnerID: "2", Command: body}, nil
	}, activity.RegisterOptions{Name: walletactivity.ActivityGetP2PCommand})
	env.RegisterActivityWithOptions(func(_ context.Context, r walletvalidation.P2PValidationRequest) (*walletvalidation.P2PValidationResult, error) {
		if r.ExpectedFeeAmount == nil || *r.ExpectedFeeAmount != 0 || r.ExpectedCurrencyUnitVersion == nil || *r.ExpectedCurrencyUnitVersion != 14 {
			t.Fatal("reviewed values not forwarded")
		}
		return nil, temporal.NewNonRetryableApplicationError("p2p_fee_changed", "p2p_validation_failed", nil)
	}, activity.RegisterOptions{Name: walletactivity.ActivityValidateP2PTransfer})
	finalized := false
	env.RegisterActivityWithOptions(func(_ context.Context, tenant, key, code string) error {
		if tenant != "tenant" || key != "key" || code != "p2p_fee_changed" {
			t.Fatal("wrong failure binding")
		}
		finalized = true
		return nil
	}, activity.RegisterOptions{Name: walletactivity.ActivityRecordP2PFailure})
	env.ExecuteWorkflow(P2P, P2PParams{TenantID: "tenant", IdempotencyKey: "key"})
	if env.GetWorkflowError() == nil || !finalized {
		t.Fatalf("failure not persisted: %v %v", env.GetWorkflowError(), finalized)
	}
}
