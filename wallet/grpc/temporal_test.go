package walletgrpc

import (
	"errors"
	"testing"

	walletworker "github.com/adonese/noebs/wallet/worker"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTemporalTaskQueueRequiresConfiguredQueue(t *testing.T) {
	server := NewServer(nil)

	_, err := server.temporalTaskQueue()
	if !errors.Is(err, walletworker.ErrMissingTaskQueue) {
		t.Fatalf("temporalTaskQueue() error = %v, want %v", err, walletworker.ErrMissingTaskQueue)
	}
}

func TestTemporalTaskQueueUsesConfiguredQueue(t *testing.T) {
	server := NewServer(nil)
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	got, err := server.temporalTaskQueue()
	if err != nil {
		t.Fatalf("temporalTaskQueue() error = %v", err)
	}
	if got != walletworker.TaskQueueMain {
		t.Fatalf("temporalTaskQueue() = %q, want %q", got, walletworker.TaskQueueMain)
	}
}

func TestMapTemporalErrorLooksThroughJoinedErrors(t *testing.T) {
	temporalErr := serviceerror.NewInvalidArgument("bad workflow input")
	err := errors.Join(temporalErr, errors.New("status write failed"))

	mapped := mapTemporalError(err)
	if status.Code(mapped) != codes.InvalidArgument {
		t.Fatalf("status.Code(mapped) = %v, want %v", status.Code(mapped), codes.InvalidArgument)
	}
}
