package dashboard

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestDecodeTransactionRowsRejectsMalformedPayload(t *testing.T) {
	_, err := decodeTransactionRows([]transactionRow{{ID: 42, Payload: "{"}})
	if err == nil {
		t.Fatal("decodeTransactionRows() error = nil, want malformed payload error")
	}
	if !strings.Contains(err.Error(), "42") {
		t.Fatalf("decodeTransactionRows() error = %v, want row id context", err)
	}
}

func TestDecodeTransactionRowsPreservesRowMetadata(t *testing.T) {
	createdAt := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)

	transactions, err := decodeTransactionRows([]transactionRow{{
		ID:        7,
		CreatedAt: sql.NullTime{Time: createdAt, Valid: true},
		UpdatedAt: sql.NullTime{Time: updatedAt, Valid: true},
		Payload:   `{"UUID":"tx-1"}`,
	}})
	if err != nil {
		t.Fatalf("decodeTransactionRows() error = %v", err)
	}
	if len(transactions) != 1 {
		t.Fatalf("transactions = %d, want 1", len(transactions))
	}
	if transactions[0].ID != 7 || transactions[0].UUID != "tx-1" {
		t.Fatalf("transaction = %+v, want id and payload fields", transactions[0])
	}
	if !transactions[0].CreatedAt.Equal(createdAt) || !transactions[0].UpdatedAt.Equal(updatedAt) {
		t.Fatalf("transaction timestamps = %s/%s", transactions[0].CreatedAt, transactions[0].UpdatedAt)
	}
}
