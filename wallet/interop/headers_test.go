package interop

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestPinnedSDKHeadersPreserveAuthority(t *testing.T) {
	var headers ProtocolHeaders
	if err := json.Unmarshal([]byte(`{"FSPIOP-Source":"bankone","fspiop-destination":"noebs","content-length":134}`), &headers); err != nil {
		t.Fatal(err)
	}
	if headers["fspiop-source"] != "bankone" || headers["content-length"] != "134" {
		t.Fatal(headers)
	}
	for _, invalid := range []string{
		`{"fspiop-source":"bankone","FSPIOP-Source":"switch"}`,
		`{"fspiop-source":"bankone","fspiop-source":"switch"}`,
		`{"fspiop-source":123}`, `{"fspiop-source":["bankone","switch"]}`,
		`{"fspiop-source":"bankone\r\nFSPIOP-Source: switch"}`,
		`{"content-length":-1}`, `{"content-length":1.5}`, `{"content-length":1e3}`,
		`{" content-length":123}`, `[]`,
	} {
		if json.Unmarshal([]byte(invalid), &headers) == nil {
			t.Fatalf("accepted ambiguous SDK headers: %s", invalid)
		}
	}
}

func TestBackendErrorsCarryRequiredSchemeCode(t *testing.T) {
	for _, test := range []struct {
		err    error
		http   int
		scheme string
	}{
		{ErrProtocol, 400, "3100"}, {walletstore.ErrInteropNotFound, 404, "3204"},
		{walletstore.ErrInteropConflict, 409, "3100"}, {walletstore.ErrInteropDisabled, 422, "3100"},
		{errors.New("database details must stay private"), 503, "2001"},
	} {
		response := httptest.NewRecorder()
		backendError(response, test.err)
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != test.http || body["statusCode"] != test.scheme || body["message"] == "" || body["message"] == test.err.Error() {
			t.Fatalf("invalid backend error: %d %v", response.Code, body)
		}
	}
}
