package walletgrpc

import (
	"errors"
	"testing"

	walletworker "github.com/adonese/noebs/wallet/worker"
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
