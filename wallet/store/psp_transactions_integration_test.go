package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
)

func TestUpdatePSPTransactionStatus_PreservesConfirmedAtAndRetryCount(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		t.Skip("docker host not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	defer func() {
		_ = container.Terminate(context.Background())
	}()

	dbName := fmt.Sprintf("noebs_wallet_store_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, "", basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	}()

	if err := basestore.Migrate(ctx, db, basestore.DefaultTenantID); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	s := New(db)
	confirmedAt := time.Now().UTC().Truncate(time.Microsecond)
	txn := PSPTransaction{
		TenantID:        "tenant",
		PSPProvider:     "noop",
		IdempotencyKey:  "idem-1",
		ClientReference: "ref-1",
		Direction:       "inbound",
		Amount:          100,
		Currency:        "USD",
		Status:          "success",
		ConfirmedAt:     sql.NullTime{Time: confirmedAt, Valid: true},
		RetryCount:      7,
	}
	if _, err := s.CreatePSPTransaction(ctx, txn); err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	update := PSPStatusUpdate{
		Status:          "success",
		ResponseMessage: sql.NullString{String: "ok", Valid: true},
	}
	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, update); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := s.GetPSPTransactionByReference(ctx, txn.TenantID, txn.ClientReference)
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if got.RetryCount != txn.RetryCount {
		t.Fatalf("expected retry_count %d, got %d", txn.RetryCount, got.RetryCount)
	}
	if !got.ConfirmedAt.Valid {
		t.Fatalf("expected confirmed_at to remain set")
	}
	if !got.ConfirmedAt.Time.Equal(confirmedAt) {
		t.Fatalf("expected confirmed_at %v, got %v", confirmedAt, got.ConfirmedAt.Time)
	}
}
