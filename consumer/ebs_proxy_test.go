package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestCallEBSRejectsReservedTenantBeforeHTTP(t *testing.T) {
	service := &Service{}
	ctx := context.Background()

	_, err := service.callEBSJSON(ctx, "default", "http://127.0.0.1:1", "/ebs", struct{}{})
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("callEBSJSON() error = %v, want %v", err, store.ErrInvalidTenantID)
	}

	_, err = service.callEBSRaw(ctx, "default", "http://127.0.0.1:1", "/ebs", []byte("{}"))
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("callEBSRaw() error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestRecordTransactionRejectsReservedTenantBeforeDB(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	err := service.recordTransaction(context.Background(), "default", ebs_fields.EBSResponse{})
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("recordTransaction() error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}
