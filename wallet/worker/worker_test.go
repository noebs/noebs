package worker

import (
	"crypto/tls"
	"errors"
	"testing"

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
