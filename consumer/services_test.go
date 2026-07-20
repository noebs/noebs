package consumer

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestGetIpinPubKeyReturnsTypedEBSCallError(t *testing.T) {
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.QRPublicKey {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				ResponseCode:    72,
				ResponseMessage: "Failed",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	service := &Service{
		Store:      &store.Store{},
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		NoebsConfig: ebs_fields.NoebsConfig{
			IPINIp:                ebsServer.URL + "/",
			EBSIPINUsername:       "ipin-user",
			KafkaTransactionTopic: testKafkaTransactionTopic,
		},
	}

	err := service.GetIpinPubKey(noConsumerTransactionContext(), "tenant-a")
	var callErr *ebs_fields.CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("GetIpinPubKey() error = %v, want CallError", err)
	}
	if callErr.Status != http.StatusBadGateway {
		t.Fatalf("CallError status = %d, want %d", callErr.Status, http.StatusBadGateway)
	}
	if callErr.Response.ResponseMessage != "Failed" {
		t.Fatalf("CallError response message = %q, want Failed", callErr.Response.ResponseMessage)
	}
	if !errors.Is(err, store.ErrMissingUUID) {
		t.Fatalf("GetIpinPubKey() error = %v, want missing UUID record error", err)
	}
}
