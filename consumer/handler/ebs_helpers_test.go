package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func TestEBSErrorDetailsExposeOnlyStatusFields(t *testing.T) {
	response := ebs_fields.EBSParserFields{
		EBSMapFields: ebs_fields.EBSMapFields{
			PaymentInfo: "private payment detail",
			LastTransactions: []ebs_fields.QRPurchase{
				{Pan: "6392561234567890"},
			},
		},
		EBSResponse: ebs_fields.EBSResponse{
			UUID:            "operation-123",
			PAN:             "6392561234567890",
			WorkingKey:      "private working key",
			ResponseMessage: "DUPLICATE_TRANSACTION",
			ResponseStatus:  "Failed",
			ResponseCode:    613,
			TranDateTime:    "200222113700",
		},
		OriginalTransaction: ebs_fields.EBSResponse{
			UUID: "private-original-operation",
			PAN:  "6392560000000000",
		},
	}

	encoded, err := json.Marshal(ebsErrorDetails(response))
	if err != nil {
		t.Fatalf("marshal error details: %v", err)
	}
	body := string(encoded)
	for _, want := range []string{
		`"UUID":"operation-123"`,
		`"responseMessage":"DUPLICATE_TRANSACTION"`,
		`"responseStatus":"Failed"`,
		`"responseCode":613`,
		`"tranDateTime":"200222113700"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("error response = %s, want %s", body, want)
		}
	}
	for _, private := range []string{
		"639256",
		"private payment detail",
		"private working key",
		"private-original-operation",
		"lastTransactions",
		"originalTransaction",
	} {
		if strings.Contains(body, private) {
			t.Fatalf("error response exposed %q: %s", private, body)
		}
	}
}

func TestCompleteEBSRedactsTransportFailure(t *testing.T) {
	const secret = "dial ebs.internal:443 with private client certificate"
	call := func(context.Context, string, struct{}) (ebs_fields.EBSParserFields, error) {
		return ebs_fields.EBSParserFields{}, &ebs_fields.CallError{
			Status: http.StatusBadGateway,
			Err:    errors.New(secret),
		}
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", func(c *fiber.Ctx) error {
		return completeEBS(c, "tenant-1", struct{}{}, call, nil)
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadGateway)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != "upstream_unavailable" || payload["message"] != "upstream service unavailable" {
		t.Fatalf("payload = %#v", payload)
	}
	if strings.Contains(payload["message"].(string), secret) {
		t.Fatalf("response exposed transport cause: %#v", payload)
	}
}
