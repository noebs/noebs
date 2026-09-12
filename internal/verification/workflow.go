package verification

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "identity-verification"
const workflowName = "identity.transition.v1"
const activityName = "identity.apply-transition.v1"

var ErrUnavailable = errors.New("verification workflow unavailable")

type Command struct {
	Action     string
	Owner      store.IdentityOwner
	SessionID  uuid.UUID
	Revision   int64
	Submission store.IdentitySubmission
	Decision   store.IdentityReviewDecisionParams
}

type Receipt struct {
	SessionID uuid.UUID `json:"session_id"`
	Revision  int64     `json:"revision"`
}

func (c Command) Validate() error {
	if _, err := store.ValidateTenantID(c.Owner.TenantID); err != nil {
		return err
	}
	if c.Owner.UserID < 1 {
		return store.ErrInvalidUserID
	}
	if c.SessionID == uuid.Nil {
		return store.ErrInvalidIdentityEvidence
	}
	switch c.Action {
	case "submit":
		return store.ValidateIdentitySubmission(c.Submission)
	case "withdraw", "discard":
		if c.Revision < 1 {
			return store.ErrInvalidIdentityEvidence
		}
		return nil
	case "decide":
		if c.Owner != c.Decision.Owner || c.SessionID != c.Decision.SessionID {
			return store.ErrInvalidIdentityEvidence
		}
		return store.ValidateIdentityReviewDecision(c.Decision)
	default:
		return store.ErrInvalidIdentityEvidence
	}
}

// Each command is durable; the store owns the serial transition and receipt.
// A changed retry reaches the store and is checked against the original terms.
func Workflow(ctx workflow.Context, command Command) (Receipt, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: time.Minute},
	})
	var result Receipt
	err := workflow.ExecuteActivity(ctx, activityName, command).Get(ctx, &result)
	return result, err
}

type Activities struct{ Store *store.Store }

func (a Activities) Apply(ctx context.Context, command Command) (Receipt, error) {
	if err := command.Validate(); err != nil {
		return Receipt{}, terminal(err)
	}
	var result store.IdentitySession
	var err error
	switch command.Action {
	case "submit":
		result, err = a.Store.SubmitIdentitySession(ctx, command.Owner, command.SessionID, command.Submission)
	case "withdraw":
		result, err = a.Store.WithdrawIdentitySession(ctx, command.Owner, command.SessionID, command.Revision)
	case "discard":
		result, err = a.Store.DiscardIdentitySession(ctx, command.Owner, command.SessionID, command.Revision)
	case "decide":
		result, err = a.Store.DecideIdentityReview(ctx, command.Decision)
	}
	if err != nil {
		return Receipt{}, terminal(err)
	}
	return Receipt{SessionID: result.ID, Revision: result.Revision}, nil
}

var businessErrors = []error{store.ErrIdentityConflict, store.ErrIdentityIncomplete, store.ErrIdentityReviewIncomplete, store.ErrInvalidIdentityEvidence, store.ErrMissingTenantID, store.ErrInvalidTenantID, store.ErrInvalidUserID, sql.ErrNoRows}

func terminal(err error) error {
	for _, known := range businessErrors {
		if errors.Is(err, known) {
			return temporal.NewNonRetryableApplicationError(known.Error(), known.Error(), nil)
		}
	}
	return err
}

func Register(w worker.Worker, s *store.Store) {
	w.RegisterWorkflowWithOptions(Workflow, workflow.RegisterOptions{Name: workflowName})
	w.RegisterActivityWithOptions(Activities{Store: s}.Apply, activity.RegisterOptions{Name: activityName})
}

type SessionReader interface {
	GetIdentitySession(context.Context, store.IdentityOwner, uuid.UUID) (store.IdentitySession, error)
}

type Client struct {
	Temporal client.Client
	Sessions SessionReader
}

func (c *Client) Execute(ctx context.Context, command Command) (store.IdentitySession, error) {
	if err := command.Validate(); err != nil {
		return store.IdentitySession{}, err
	}
	if c == nil || c.Temporal == nil || c.Sessions == nil {
		return store.IdentitySession{}, ErrUnavailable
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return store.IdentitySession{}, err
	}
	hash := sha256.Sum256(payload)
	id := fmt.Sprintf("identity/%s/%d/%s/%s/%s", command.Owner.TenantID, command.Owner.UserID, command.SessionID, command.Action, hex.EncodeToString(hash[:]))
	run, err := c.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: id, TaskQueue: TaskQueue,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, workflowName, command)
	if err != nil {
		return store.IdentitySession{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	err = run.Get(ctx, nil)
	var application *temporal.ApplicationError
	if errors.As(err, &application) {
		for _, known := range businessErrors {
			if application.Type() == known.Error() {
				return store.IdentitySession{}, known
			}
		}
	}
	if err != nil {
		return store.IdentitySession{}, err
	}
	// A retry can reuse a completed command after another transition has run.
	// Return the current case instead of that workflow's historical snapshot.
	return c.Sessions.GetIdentitySession(ctx, command.Owner, command.SessionID)
}
