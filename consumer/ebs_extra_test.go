package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestQRPurchaseTransactionValidation(t *testing.T) {
	valid := ebs_fields.QRPurchase{
		UUID:            "qr-tx-1",
		MerchantID:      "merchant-1",
		ResponseCode:    0,
		ResponseMessage: "Approved",
	}
	if _, err := qrPurchaseTransaction("", valid); !errors.Is(err, ErrMissingMerchantID) {
		t.Fatalf("missing request merchant error = %v, want %v", err, ErrMissingMerchantID)
	}
	missingUUID := valid
	missingUUID.UUID = " "
	if _, err := qrPurchaseTransaction("merchant-1", missingUUID); !errors.Is(err, store.ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, store.ErrMissingUUID)
	}
	mismatch := valid
	mismatch.MerchantID = "merchant-2"
	if _, err := qrPurchaseTransaction("merchant-1", mismatch); !errors.Is(err, ErrInvalidMerchantID) {
		t.Fatalf("merchant mismatch error = %v, want %v", err, ErrInvalidMerchantID)
	}
	withoutItemMerchant := valid
	withoutItemMerchant.MerchantID = ""
	got, err := qrPurchaseTransaction("merchant-1", withoutItemMerchant)
	if err != nil {
		t.Fatalf("qrPurchaseTransaction() error = %v", err)
	}
	if got.MerchantID != "merchant-1" || got.UUID != "qr-tx-1" {
		t.Fatalf("qrPurchaseTransaction() = %+v", got)
	}
}

func TestQRTransactionsRecordsLastTransactions(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	ctx := context.Background()

	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.MerchantTransactionStatus {
			t.Fatalf("EBS path = %s, want /%s", r.URL.Path, ebs_fields.MerchantTransactionStatus)
		}
		var req ebs_fields.ConsumerQRStatus
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode EBS request: %v", err)
		}
		if req.MerchantID != "merchant-1" {
			t.Fatalf("request merchant = %q, want merchant-1", req.MerchantID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSMapFields: ebs_fields.EBSMapFields{
				LastTransactions: []ebs_fields.QRPurchase{{
					UUID:            "qr-tx-1",
					MerchantID:      "merchant-1",
					MerchantName:    "Merchant One",
					Pan:             "9222081700000000",
					ResponseCode:    0,
					ResponseMessage: "Approved",
					ResponseStatus:  "Successful",
					TranAmount:      1250,
					TranDateTime:    "20260531120000",
					TransactionID:   "transaction-1",
				}},
			},
			EBSResponse: ebs_fields.EBSResponse{
				ResponseCode:    0,
				ResponseMessage: "Successful",
				ResponseStatus:  "Successful",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: ebsServer.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			ConsumerID:            "consumer-app",
			KafkaTransactionTopic: testKafkaTransactionTopic,
		},
	}

	res, err := service.QRTransactions(transactionActorContext(t, 42), tenantID, ebs_fields.ConsumerQRStatus{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: "consumer-app",
			TranDateTime:  "20260531115959",
			UUID:          "qr-status-request",
		},
		MerchantID: "merchant-1",
	})
	if err != nil {
		t.Fatalf("QRTransactions() error = %v", err)
	}
	if len(res.LastTransactions) != 1 {
		t.Fatalf("lastTransactions = %d, want 1", len(res.LastTransactions))
	}

	stored, err := storeSvc.GetTransactionByUUID(ctx, tenantID, "qr-tx-1")
	if err != nil {
		t.Fatalf("GetTransactionByUUID(qr-tx-1): %v", err)
	}
	if stored.MerchantID != "merchant-1" || stored.PAN != "922208*****0000" || stored.TranAmount != 1250 {
		t.Fatalf("stored transaction = %+v", stored)
	}
}
