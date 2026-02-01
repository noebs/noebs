package walletgrpc

import (
	"context"

	walletworker "github.com/adonese/noebs/wallet/worker"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type temporalClient interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error
}

func (s *Server) ensureTemporalClient() (temporalClient, error) {
	if s == nil {
		return nil, walletworker.ErrMissingTemporalHost
	}
	if s.TemporalClient != nil {
		return s.TemporalClient, nil
	}
	s.temporalOnce.Do(func() {
		opts := s.TemporalOptions
		if opts.TaskQueue == "" {
			opts.TaskQueue = walletworker.TaskQueueMain
		}
		client, err := walletworker.NewClient(opts)
		if err != nil {
			s.temporalErr = err
			return
		}
		s.TemporalClient = client
	})
	if s.TemporalClient != nil {
		return s.TemporalClient, nil
	}
	if s.temporalErr != nil {
		return nil, s.temporalErr
	}
	return nil, walletworker.ErrMissingTemporalHost
}

func mapTemporalError(err error) error {
	if err == nil {
		return nil
	}
	switch err.(type) {
	case *serviceerror.NotFound:
		return status.Error(codes.NotFound, err.Error())
	case *serviceerror.InvalidArgument:
		return status.Error(codes.InvalidArgument, err.Error())
	case *serviceerror.WorkflowExecutionAlreadyStarted:
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
