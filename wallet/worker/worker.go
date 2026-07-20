package worker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

var (
	ErrMissingTemporalHost        = errors.New("missing temporal host")
	ErrMissingTemporalPort        = errors.New("missing temporal port")
	ErrMissingTemporalNamespace   = errors.New("missing temporal namespace")
	ErrMissingTaskQueue           = errors.New("missing temporal task queue")
	ErrMissingTemporalTLS         = errors.New("missing temporal TLS configuration")
	ErrMissingTemporalCredentials = errors.New("missing temporal credentials")
)

type Options struct {
	Host        string
	Port        string
	Namespace   string
	TaskQueue   TaskQueue
	TLS         *tls.Config
	Credentials client.Credentials
}

func (o Options) Address() (string, error) {
	if o.Host == "" {
		return "", ErrMissingTemporalHost
	}
	if o.Port == "" {
		return "", ErrMissingTemporalPort
	}
	return fmt.Sprintf("%s:%s", o.Host, o.Port), nil
}

func (o Options) Validate() error {
	if o.TaskQueue == "" {
		return ErrMissingTaskQueue
	}
	return o.validateConnection()
}

func (o Options) validateConnection() error {
	if o.Namespace == "" {
		return ErrMissingTemporalNamespace
	}
	if _, err := o.Address(); err != nil {
		return err
	}
	if o.TLS == nil {
		return ErrMissingTemporalTLS
	}
	if o.Credentials == nil {
		return ErrMissingTemporalCredentials
	}
	return nil
}

type Runner struct {
	Client client.Client
	Worker worker.Worker
}

func NewClient(opts Options) (client.Client, error) {
	clientOptions, err := temporalClientOptions(opts)
	if err != nil {
		return nil, err
	}
	return client.Dial(clientOptions)
}

func NewNamespaceClient(opts Options) (client.NamespaceClient, error) {
	clientOptions, err := temporalClientOptions(opts)
	if err != nil {
		return nil, err
	}
	return client.NewNamespaceClient(clientOptions)
}

func temporalClientOptions(opts Options) (client.Options, error) {
	if err := opts.validateConnection(); err != nil {
		return client.Options{}, err
	}
	address, err := opts.Address()
	if err != nil {
		return client.Options{}, err
	}
	return client.Options{
		HostPort:    address,
		Namespace:   opts.Namespace,
		Credentials: opts.Credentials,
		ConnectionOptions: client.ConnectionOptions{
			TLS: opts.TLS.Clone(),
		},
	}, nil
}

func NewRunner(ctx context.Context, opts Options, register func(worker.Worker)) (*Runner, error) {
	_ = ctx
	if err := opts.Validate(); err != nil {
		return nil, err
	}
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
