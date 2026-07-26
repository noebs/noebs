package worker

import (
	"crypto/tls"
	"errors"
	"testing"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	"go.temporal.io/sdk/client"
)

func TestOptionsAddressRequiresExplicitHostAndPort(t *testing.T) {
	_, err := (Options{Port: "7233"}).Address()
	if !errors.Is(err, ErrMissingTemporalHost) {
		t.Fatalf("Address() host error = %v, want %v", err, ErrMissingTemporalHost)
	}

	_, err = (Options{Host: "temporal-frontend"}).Address()
	if !errors.Is(err, ErrMissingTemporalPort) {
		t.Fatalf("Address() port error = %v, want %v", err, ErrMissingTemporalPort)
	}
}

func TestOptionsAddressUsesExplicitHostAndPort(t *testing.T) {
	got, err := (Options{Host: "temporal-frontend", Port: "7233"}).Address()
	if err != nil {
		t.Fatalf("Address() error = %v", err)
	}
	if got != "temporal-frontend:7233" {
		t.Fatalf("Address() = %q, want temporal-frontend:7233", got)
	}
}

func TestOptionsValidateRequiresExplicitRuntimeFields(t *testing.T) {
	opts := Options{Host: "temporal-frontend", Port: "7233", TaskQueue: TaskQueueMain}
	if err := opts.Validate(); !errors.Is(err, ErrMissingTemporalNamespace) {
		t.Fatalf("Validate() namespace error = %v, want %v", err, ErrMissingTemporalNamespace)
	}

	opts = Options{Host: "temporal-frontend", Port: "7233", Namespace: "default"}
	if err := opts.Validate(); !errors.Is(err, ErrMissingTaskQueue) {
		t.Fatalf("Validate() task queue error = %v, want %v", err, ErrMissingTaskQueue)
	}

	opts.TaskQueue = TaskQueueMain
	if err := opts.Validate(); !errors.Is(err, ErrMissingTemporalTLS) {
		t.Fatalf("Validate() TLS error = %v, want %v", err, ErrMissingTemporalTLS)
	}

	opts.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	if err := opts.Validate(); !errors.Is(err, ErrMissingTemporalCredentials) {
		t.Fatalf("Validate() credentials error = %v, want %v", err, ErrMissingTemporalCredentials)
	}

	opts.Credentials = client.NewAPIKeyStaticCredentials("test-token")
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRegisterDepsRequiresEveryActivityGroup(t *testing.T) {
	store := &walletstore.Store{}
	psp := &walletactivity.PSPActivities{}
	fx := &walletactivity.FXActivities{}
	tests := []struct {
		name string
		deps RegisterDeps
		want error
	}{
		{
			name: "store",
			deps: RegisterDeps{PSPActivities: psp, FXActivities: fx},
			want: ErrMissingWalletStore,
		},
		{
			name: "PSP activities",
			deps: RegisterDeps{Store: store, FXActivities: fx},
			want: ErrMissingPSPActivities,
		},
		{
			name: "FX activities",
			deps: RegisterDeps{Store: store, PSPActivities: psp},
			want: ErrMissingFXActivities,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.deps.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
	if err := (RegisterDeps{Store: store, PSPActivities: psp, FXActivities: fx}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRegisterWalletRejectsMissingWorker(t *testing.T) {
	err := RegisterWallet(nil, RegisterDeps{
		Store:         &walletstore.Store{},
		PSPActivities: &walletactivity.PSPActivities{},
		FXActivities:  &walletactivity.FXActivities{},
	})
	if !errors.Is(err, ErrMissingWorker) {
		t.Fatalf("RegisterWallet() error = %v, want %v", err, ErrMissingWorker)
	}
}

func TestNewRunnerRequiresRegistrarBeforeDial(t *testing.T) {
	opts := Options{
		Host:        "temporal.invalid",
		Port:        "7233",
		Namespace:   "default",
		TaskQueue:   TaskQueueMain,
		TLS:         &tls.Config{MinVersion: tls.VersionTLS13},
		Credentials: client.NewAPIKeyStaticCredentials("test-token"),
	}
	_, err := NewRunner(t.Context(), opts, nil)
	if !errors.Is(err, ErrMissingRegistrar) {
		t.Fatalf("NewRunner() error = %v, want %v", err, ErrMissingRegistrar)
	}
}
