package worker

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

var (
	ErrMissingTemporalHost      = errors.New("missing temporal host")
	ErrMissingTemporalNamespace = errors.New("missing temporal namespace")
	ErrMissingTaskQueue         = errors.New("missing temporal task queue")
)

type Options struct {
	Host      string
	Port      string
	Namespace string
	TaskQueue TaskQueue
}

func (o Options) Address() (string, error) {
	if o.Host == "" {
		return "", ErrMissingTemporalHost
	}
	if o.Port == "" {
		return o.Host, nil
	}
	return fmt.Sprintf("%s:%s", o.Host, o.Port), nil
}

func (o Options) Validate() error {
	if o.Namespace == "" {
		return ErrMissingTemporalNamespace
	}
	if o.TaskQueue == "" {
		return ErrMissingTaskQueue
	}
	_, err := o.Address()
	return err
}

type Runner struct {
	Client client.Client
	Worker worker.Worker
}

func NewClient(opts Options) (client.Client, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	address, err := opts.Address()
	if err != nil {
		return nil, err
	}
	return client.Dial(client.Options{
		HostPort:  address,
		Namespace: opts.Namespace,
	})
}

func NewRunner(ctx context.Context, opts Options, register func(worker.Worker)) (*Runner, error) {
	_ = ctx
	c, err := NewClient(opts)
	if err != nil {
		return nil, err
	}
	w := worker.New(c, string(opts.TaskQueue), worker.Options{})
	if register != nil {
		register(w)
	}
	return &Runner{Client: c, Worker: w}, nil
}

func (r *Runner) Start() error {
	if r == nil || r.Worker == nil {
		return errors.New("worker not initialized")
	}
	return r.Worker.Start()
}

func (r *Runner) Stop() {
	if r == nil {
		return
	}
	if r.Worker != nil {
		r.Worker.Stop()
	}
	if r.Client != nil {
		r.Client.Close()
	}
}
