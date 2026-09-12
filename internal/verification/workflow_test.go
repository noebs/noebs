package verification

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestWorkflowRetriesStorageFailureAndReturnsCommittedState(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	count := 0
	id := uuid.New()
	env.RegisterActivityWithOptions(func(context.Context, Command) (Receipt, error) {
		count++
		if count == 1 {
			return Receipt{}, errors.New("database restarting")
		}
		return Receipt{SessionID: id, Revision: 5}, nil
	}, activity.RegisterOptions{Name: activityName})
	env.ExecuteWorkflow(Workflow, Command{})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result Receipt
	if err := env.GetWorkflowResult(&result); err != nil || count != 2 || result.SessionID != id || result.Revision != 5 {
		t.Fatalf("result=%+v attempts=%d error=%v", result, count, err)
	}
}

func TestWorkflowDoesNotRetryRejectedTransition(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	count := 0
	env.RegisterActivityWithOptions(func(context.Context, Command) (Receipt, error) {
		count++
		return Receipt{}, terminal(store.ErrIdentityConflict)
	}, activity.RegisterOptions{Name: activityName})
	env.ExecuteWorkflow(Workflow, Command{})
	var application *temporal.ApplicationError
	if !errors.As(env.GetWorkflowError(), &application) || !application.NonRetryable() || count != 1 {
		t.Fatalf("attempts=%d error=%v", count, env.GetWorkflowError())
	}
}

type sessionReaderFunc func(context.Context, store.IdentityOwner, uuid.UUID) (store.IdentitySession, error)

func (f sessionReaderFunc) GetIdentitySession(ctx context.Context, owner store.IdentityOwner, id uuid.UUID) (store.IdentitySession, error) {
	return f(ctx, owner, id)
}

func TestClientReloadsCurrentCaseWhenCompletedCommandIsReused(t *testing.T) {
	command := Command{Action: "withdraw", Owner: store.IdentityOwner{TenantID: "tenant", UserID: 7}, SessionID: uuid.New(), Revision: 5}
	connection := &mocks.Client{}
	run := &mocks.WorkflowRun{}
	connection.On("ExecuteWorkflow", mock.Anything, mock.Anything, workflowName, command).Return(run, nil).Twice()
	run.On("Get", mock.Anything, mock.Anything).Return(nil).Twice()
	current := store.IdentitySession{ID: command.SessionID, Status: "withdrawn", Revision: 6}
	c := &Client{Temporal: connection, Sessions: sessionReaderFunc(func(_ context.Context, owner store.IdentityOwner, id uuid.UUID) (store.IdentitySession, error) {
		if owner != command.Owner || id != command.SessionID {
			t.Fatal("reader lost command ownership")
		}
		return current, nil
	})}
	first, err := c.Execute(t.Context(), command)
	if err != nil || first.Revision != 6 {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	current.Revision = 7
	second, err := c.Execute(t.Context(), command)
	if err != nil || second.Revision != 7 {
		t.Fatalf("replayed command returned obsolete state: %+v error=%v", second, err)
	}
	connection.AssertExpectations(t)
	run.AssertExpectations(t)
}

func TestCommandValidationPrecedesTemporalAndDoesNotDefaultOwner(t *testing.T) {
	command := Command{Action: "withdraw", Owner: store.IdentityOwner{TenantID: "tenant", UserID: 1}, SessionID: uuid.New(), Revision: 1}
	var c *Client
	for _, test := range []struct {
		name   string
		mutate func(*Command)
		want   error
	}{
		{"tenant", func(c *Command) { c.Owner.TenantID = "" }, store.ErrMissingTenantID},
		{"user", func(c *Command) { c.Owner.UserID = 0 }, store.ErrInvalidUserID},
		{"revision", func(c *Command) { c.Revision = 0 }, store.ErrInvalidIdentityEvidence},
		{"session", func(c *Command) { c.SessionID = uuid.Nil }, store.ErrInvalidIdentityEvidence},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := command
			test.mutate(&input)
			_, err := c.Execute(t.Context(), input)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := c.Execute(t.Context(), command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing Temporal=%v", err)
	}
}
