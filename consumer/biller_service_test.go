package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestGetBillerReadsCachedBillerOnly(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	service := &Service{Store: storeSvc}
	ctx := context.Background()

	if err := storeSvc.UpsertCacheBiller(ctx, tenantID, "0912141660", "0010010002"); err != nil {
		t.Fatalf("seed cached biller: %v", err)
	}
	got, err := service.GetBiller(ctx, tenantID, "0912141660")
	if err != nil {
		t.Fatalf("get cached biller: %v", err)
	}
	if got != "0010010002" {
		t.Fatalf("biller = %q, want 0010010002", got)
	}

	if _, err := service.GetBiller(ctx, tenantID, "0990000000"); err == nil || !store.ErrNotFound(err) {
		t.Fatalf("missing cached biller error = %v, want not found", err)
	}
}

func TestGetBillsUsesExplicitPayeeIDAndDoesNotChangeCacheOnEBSError(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	ctx := context.Background()
	if err := storeSvc.UpsertCacheBiller(ctx, tenantID, "0912141660", "0010010001"); err != nil {
		t.Fatalf("seed cached biller: %v", err)
	}

	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.ConsumerBillInquiryEndpoint {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read EBS request: %v", err)
		}
		if !bytes.Contains(body, []byte(`"payeeId":"0010010002"`)) {
			t.Fatalf("EBS request did not use explicit payee_id: %s", body)
		}
		if bytes.Contains(body, []byte(`"payeeId":"0010010001"`)) {
			t.Fatalf("EBS request used cached biller id: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				UUID:            "bill-inquiry-error",
				ResponseCode:    5,
				ResponseMessage: "No bill",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: testHTTPClient(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			ConsumerID:            "consumer-app",
			EBSConsumerKey:        "test-key",
			BillInquiryIPIN:       "0000",
			BillInquiryPAN:        "9222081700009999",
			BillInquiryExpDate:    "2601",
			KafkaTransactionTopic: testKafkaTransactionTopic,
		},
	}

	_, _, err := service.GetBills(ctx, tenantID, Bills{
		Phone:   "0912141660",
		PayeeID: "0010010002",
	})
	if err == nil {
		t.Fatalf("GetBills() error = nil, want EBS error")
	}
	cached, err := storeSvc.GetCacheBiller(ctx, tenantID, "0912141660")
	if err != nil {
		t.Fatalf("get cached biller after failed inquiry: %v", err)
	}
	if cached.BillerID != "0010010001" {
		t.Fatalf("cached biller changed to %q, want 0010010001", cached.BillerID)
	}
}

func TestGetBillsRequiresExplicitPayeeID(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	_, _, err := service.GetBills(context.Background(), "tenant-a", Bills{Phone: "0912141660"})
	if !errors.Is(err, ErrMissingBillerID) {
		t.Fatalf("missing payee_id error = %v, want %v", err, ErrMissingBillerID)
	}
}
