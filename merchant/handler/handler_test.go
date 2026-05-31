package handler

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/store"
)

func TestNewRequiresService(t *testing.T) {
	h, err := New(nil)
	if h != nil {
		t.Fatalf("handler = %#v, want nil", h)
	}
	if !errors.Is(err, merchant.ErrMissingService) {
		t.Fatalf("err = %v, want %v", err, merchant.ErrMissingService)
	}
}

func TestNewRequiresStore(t *testing.T) {
	h, err := New(&merchant.Service{})
	if h != nil {
		t.Fatalf("handler = %#v, want nil", h)
	}
	if !errors.Is(err, merchant.ErrMissingStore) {
		t.Fatalf("err = %v, want %v", err, merchant.ErrMissingStore)
	}
}

func TestNew(t *testing.T) {
	service := &merchant.Service{Store: &store.Store{}}
	h, err := New(service)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if h == nil || h.Service != service {
		t.Fatalf("handler service = %#v, want %#v", h, service)
	}
}
