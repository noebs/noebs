package store

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := OpenFromConfig("", ":memory:", "sqlite")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

func TestStore_EnsureTenant_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.EnsureTenant(context.Background(), "")
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_CreateUser_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateUser(context.Background(), "", &ebs_fields.User{Mobile: "0990000000"})
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_CreateUser_MissingUser(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateUser(context.Background(), "t1", nil)
	if !errors.Is(err, ErrMissingUser) {
		t.Fatalf("expected ErrMissingUser, got %v", err)
	}
}

func TestStore_CreateToken_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateToken(context.Background(), "", &ebs_fields.Token{UUID: "u1"})
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_CreateToken_MissingUUID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateToken(context.Background(), "t1", &ebs_fields.Token{})
	if !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("expected ErrMissingUUID, got %v", err)
	}
}
