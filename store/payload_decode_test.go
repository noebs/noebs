package store

import (
	"math"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestDecodeStoredTransactionPayloadRejectsMalformedJSON(t *testing.T) {
	_, err := decodeStoredTransactionPayload("{", `uuid "tx-1"`)
	if err == nil {
		t.Fatal("decodeStoredTransactionPayload() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), `uuid "tx-1"`) {
		t.Fatalf("decodeStoredTransactionPayload() error = %v, want label context", err)
	}
}

func TestDecodeStoredTransactionPayloadDecodesValidJSON(t *testing.T) {
	got, err := decodeStoredTransactionPayload(`{"UUID":"tx-1"}`, `uuid "tx-1"`)
	if err != nil {
		t.Fatalf("decodeStoredTransactionPayload() error = %v", err)
	}
	if got.UUID != "tx-1" {
		t.Fatalf("UUID = %q, want tx-1", got.UUID)
	}
}

func TestMarshalTransactionPayloadRejectsUnsupportedValues(t *testing.T) {
	_, err := marshalTransactionPayload(ebs_fields.EBSResponse{
		UUID:       "tx-1",
		TranAmount: float32(math.NaN()),
	})
	if err == nil {
		t.Fatal("marshalTransactionPayload() error = nil, want unsupported value error")
	}
	if !strings.Contains(err.Error(), "marshal transaction payload") {
		t.Fatalf("marshalTransactionPayload() error = %v, want context", err)
	}
}

func TestUpsertTransactionProjectionRejectsUnmarshalablePayloadBeforeDB(t *testing.T) {
	err := (&Store{}).UpsertTransactionProjection(t.Context(), "tenant", ebs_fields.EBSResponse{
		UUID:       "tx-1",
		TranAmount: float32(math.NaN()),
	})
	if err == nil {
		t.Fatal("UpsertTransactionProjection() error = nil, want marshal error")
	}
	if !strings.Contains(err.Error(), "marshal transaction payload") {
		t.Fatalf("UpsertTransactionProjection() error = %v, want marshal context", err)
	}
}
