package handler

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/store"
)

func TestNewRequiresService(t *testing.T) {
	h, err := New(nil)
	if h != nil {
		t.Fatalf("handler = %#v, want nil", h)
	}
	if !errors.Is(err, consumer.ErrMissingService) {
		t.Fatalf("err = %v, want %v", err, consumer.ErrMissingService)
	}
}

func TestNewRequiresStore(t *testing.T) {
	h, err := New(&consumer.Service{})
	if h != nil {
		t.Fatalf("handler = %#v, want nil", h)
	}
	if !errors.Is(err, consumer.ErrMissingStore) {
		t.Fatalf("err = %v, want %v", err, consumer.ErrMissingStore)
	}
}

func TestNew(t *testing.T) {
	service := &consumer.Service{Store: &store.Store{}}
	h, err := New(service)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if h == nil || h.Service != service {
		t.Fatalf("handler service = %#v, want %#v", h, service)
	}
}
