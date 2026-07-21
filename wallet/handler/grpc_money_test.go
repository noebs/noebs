package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/groosh"
	walletmoney "github.com/adonese/noebs/wallet/money"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func TestProtoJSONResponseSerializesInt64AsExactStrings(t *testing.T) {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", func(c *fiber.Ctx) error {
		return protoJSONResponse(c, http.StatusOK, &walletv1.Wallet{
			Balance:          9_007_199_254_740_993,
			AvailableBalance: 9_007_199_254_740_994,
			BalanceMoney: &walletv1.MoneyAmount{
				MinorUnits:            "9007199254740993",
				CurrencyCode:          "USD",
				CurrencyUnitVersionId: 9_007_199_254_740_995,
			},
		})
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer closeWalletResponseBody(t, response.Body)
	payload := decodeJSONObject(t, response.Body)
	if payload["balance"] != "9007199254740993" || payload["available_balance"] != "9007199254740994" {
		t.Fatalf("legacy integer fields = %#v/%#v, want exact strings", payload["balance"], payload["available_balance"])
	}
	balanceMoney, ok := payload["balance_money"].(map[string]any)
	if !ok {
		t.Fatalf("balance_money = %#v", payload["balance_money"])
	}
	if balanceMoney["currency_unit_version_id"] != "9007199254740995" || balanceMoney["minor_units"] != "9007199254740993" {
		t.Fatalf("balance_money = %#v, want exact identifiers and minor units", balanceMoney)
	}
	if exponent, present := balanceMoney["minor_exponent"]; !present || exponent != float64(0) {
		t.Fatalf("zero minor_exponent = %#v (present %t), want explicit 0", exponent, present)
	}
}

func TestWalletFrontendMappingPreservesLegacyWireTypesAndAddsExactMoney(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	money := &walletv1.MoneyAmount{
		MinorUnits:            "9007199254740993",
		CurrencyCode:          "USD",
		CurrencyUnitVersionId: 9_007_199_254_740_995,
		MinorExponent:         2,
		MajorUnits:            "90071992547409.93",
		Display:               "90071992547409.93",
		Canonical:             "USD@9007199254740995:9007199254740993",
	}
	wallet, err := walletResponseFromProto(&walletv1.Wallet{
		Id:                    "550e8400-e29b-41d4-a716-446655440000",
		OwnerType:             "user",
		OwnerId:               "42",
		Currency:              "USD",
		Balance:               123,
		AvailableBalance:      122,
		BalanceMoney:          money,
		AvailableBalanceMoney: money,
		CreatedAt:             now,
		UpdatedAt:             now,
	})
	if err != nil {
		t.Fatalf("walletResponseFromProto() error = %v", err)
	}
	payloadBytes, err := json.Marshal(wallet)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["balance"] != float64(123) || payload["user_id"] != float64(42) {
		t.Fatalf("wallet legacy numeric fields = %#v", payload)
	}
	balanceMoney := payload["balance_money"].(map[string]any)
	if balanceMoney["currency_unit_version_id"] != "9007199254740995" || balanceMoney["minor_units"] != "9007199254740993" {
		t.Fatalf("balance_money = %#v", balanceMoney)
	}

	entry, err := walletTransactionResponseFromProto(&walletv1.WalletLedgerEntry{
		Id:                91,
		TransactionId:     92,
		Amount:            93,
		BalanceAfter:      94,
		WalletSequence:    95,
		AmountMoney:       money,
		BalanceAfterMoney: money,
		CreatedAt:         now,
	})
	if err != nil {
		t.Fatalf("walletTransactionResponseFromProto() error = %v", err)
	}
	payloadBytes, err = json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != float64(91) || payload["transaction_id"] != float64(92) || payload["amount"] != float64(93) {
		t.Fatalf("ledger legacy numeric fields = %#v", payload)
	}
	amountMoney := payload["amount_money"].(map[string]any)
	if amountMoney["minor_units"] != "9007199254740993" || amountMoney["currency_unit_version_id"] != "9007199254740995" {
		t.Fatalf("ledger exact money = %#v", amountMoney)
	}
}

func TestPaymentMethodMappingsKeepLegacyBoundsAndAddExactMoneyAboveJavaScriptSafeInteger(t *testing.T) {
	const unitID int64 = 9_007_199_254_740_993
	const minAmount int64 = 9_007_199_254_740_993
	const maxAmount int64 = 9_007_199_254_740_994
	exponent := uint8(2)
	effectiveFrom := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        unitID,
		Code:             "USD",
		ISOMinorExponent: &exponent,
		DisplayExponent:  &exponent,
		CashExponent:     &exponent,
		CashIncrement:    1,
		EffectiveFrom:    effectiveFrom,
	})
	if err != nil {
		t.Fatalf("NewCurrencyUnit() error = %v", err)
	}
	currency := walletmoney.Currency{
		Definition: walletstore.CurrencyUnitVersion{
			ID: unitID, CurrencyCode: "USD", ValidFrom: effectiveFrom,
		},
		Unit: unit,
	}
	direct, err := paymentMethodResponseFromModel(walletstore.PSPPaymentMethod{
		ProviderCode:   "direct-psp",
		Currencies:     []string{"USD"},
		CurrencyUnitID: unitID,
		MinAmount:      sql.NullInt64{Int64: minAmount, Valid: true},
		MaxAmount:      sql.NullInt64{Int64: maxAmount, Valid: true},
	}, &currency)
	if err != nil {
		t.Fatalf("paymentMethodResponseFromModel() error = %v", err)
	}
	responses := []paymentMethodResponse{
		paymentMethodResponseFromProto(&walletv1.PaymentMethod{
			ProviderCode: "grpc-psp", CurrencyUnitVersionId: unitID,
			MinAmount: proto.Int64(minAmount), MaxAmount: proto.Int64(maxAmount),
			MinAmountMoney: &walletv1.MoneyAmount{
				MinorUnits: "9007199254740993", CurrencyCode: "USD", CurrencyUnitVersionId: unitID,
			},
			MaxAmountMoney: &walletv1.MoneyAmount{
				MinorUnits: "9007199254740994", CurrencyCode: "USD", CurrencyUnitVersionId: unitID,
			},
		}),
		direct,
	}
	for _, response := range responses {
		payload, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["currency_unit_version_id"] != "9007199254740993" {
			t.Fatalf("payment method JSON = %s, want exact string unit ID", payload)
		}
		// The existing fields remain JSON numbers for wire compatibility. The
		// companion money fields are the safe interface for exact consumers.
		if !bytes.Contains(payload, []byte(`"min_amount":9007199254740993`)) ||
			!bytes.Contains(payload, []byte(`"max_amount":9007199254740994`)) {
			t.Fatalf("payment method legacy bounds changed wire shape: %s", payload)
		}
		minMoney, ok := decoded["min_amount_money"].(map[string]any)
		if !ok || minMoney["minor_units"] != "9007199254740993" ||
			minMoney["currency_unit_version_id"] != "9007199254740993" {
			t.Fatalf("payment method exact minimum = %#v in %s", decoded["min_amount_money"], payload)
		}
		maxMoney, ok := decoded["max_amount_money"].(map[string]any)
		if !ok || maxMoney["minor_units"] != "9007199254740994" ||
			maxMoney["currency_unit_version_id"] != "9007199254740993" {
			t.Fatalf("payment method exact maximum = %#v in %s", decoded["max_amount_money"], payload)
		}
	}
}

func TestQuoteConversionHandlerRejectsClientRoundingPolicy(t *testing.T) {
	client := &moneyCaptureClient{}
	handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/wallet/fx/quotes", handler.QuoteConversion)
	request := httptest.NewRequest(http.MethodPost, "/wallet/fx/quotes", bytes.NewBufferString(`{
		"idempotency_key":"quote-rounding-1",
		"source_code":"ecb-reference",
		"base_currency":"EUR",
		"quote_currency":"USD",
		"input_minor_units":"100",
		"side":"mid",
		"rounding_mode":"floor"
	}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWalletResponseBody(t, response.Body)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if client.quoteRequest != nil {
		t.Fatalf("client received rejected quote request: %+v", client.quoteRequest)
	}
}

func TestMoneyHandlersRejectUnknownFieldsAndTrailingJSON(t *testing.T) {
	for _, body := range []string{
		`{"currency_code":"USD","currency_unit_version_id":"1","major_units":"1.00","rounding_mode":"half_even","currency_unut":"typo"}`,
		`{"currency_code":"USD","currency_unit_version_id":"1","major_units":"1.00","rounding_mode":"half_even"} {}`,
	} {
		client := &moneyCaptureClient{}
		handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		app.Post("/wallet/money/parse", handler.ParseMoney)
		request := httptest.NewRequest(http.MethodPost, "/wallet/money/parse", bytes.NewBufferString(body))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		closeWalletResponseBody(t, response.Body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d for %s", response.StatusCode, http.StatusBadRequest, body)
		}
		if client.parseRequest != nil {
			t.Fatalf("client received invalid request: %+v", client.parseRequest)
		}
	}
}

func TestCurrencyUnitIDsAreCanonicalDecimalStringsAtFrontendBoundary(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{value: "1", want: 1},
		{value: "9007199254740993", want: 9_007_199_254_740_993},
		{value: "9223372036854775807", want: 9_223_372_036_854_775_807},
	}
	for _, test := range tests {
		got, err := parseCanonicalCurrencyUnitID(test.value)
		if err != nil || got != test.want {
			t.Fatalf("parseCanonicalCurrencyUnitID(%q) = %d, %v; want %d", test.value, got, err, test.want)
		}
	}
	if _, err := parseCanonicalCurrencyUnitID(""); !errors.Is(err, walletstore.ErrMissingCurrencyUnitID) {
		t.Fatalf("empty unit ID error = %v, want %v", err, walletstore.ErrMissingCurrencyUnitID)
	}
	for _, value := range []string{"0", "-1", "+1", "01", " 1", "1 ", "9223372036854775808"} {
		if _, err := parseCanonicalCurrencyUnitID(value); !errors.Is(err, walletstore.ErrInvalidCurrencyUnitID) {
			t.Fatalf("parseCanonicalCurrencyUnitID(%q) error = %v, want %v", value, err, walletstore.ErrInvalidCurrencyUnitID)
		}
	}
}

func TestQuoteConversionHandlerForwardsExactStringCurrencyUnitIDs(t *testing.T) {
	client := &moneyCaptureClient{}
	handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/wallet/fx/quotes", gateway.InternalUserIdentityMiddleware(), handler.QuoteConversion)
	request := httptest.NewRequest(http.MethodPost, "/wallet/fx/quotes", bytes.NewBufferString(`{
		"idempotency_key":"quote-exact-units-1",
		"source_code":"ecb-reference",
		"base_currency":"EUR",
		"base_currency_unit_version_id":"9007199254740993",
		"quote_currency":"USD",
		"quote_currency_unit_version_id":"9007199254740994",
		"input_minor_units":"100",
		"side":"mid"
	}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	setWalletPrincipalHeaders(request, walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour)))

	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWalletResponseBody(t, response.Body)
	if response.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusCreated, payload)
	}
	if client.quoteRequest == nil {
		t.Fatal("quote request was not forwarded")
	}
	if client.quoteRequest.GetBaseCurrencyUnitVersionId() != 9_007_199_254_740_993 ||
		client.quoteRequest.GetQuoteCurrencyUnitVersionId() != 9_007_199_254_740_994 {
		t.Fatalf("forwarded unit IDs = %d/%d", client.quoteRequest.GetBaseCurrencyUnitVersionId(), client.quoteRequest.GetQuoteCurrencyUnitVersionId())
	}
	if client.quoteRequest.GetIdempotencyKey() != "quote-exact-units-1" {
		t.Fatalf("forwarded idempotency key = %q", client.quoteRequest.GetIdempotencyKey())
	}
}

func TestParseAndFormatHandlersForwardExactStringCurrencyUnitIDs(t *testing.T) {
	client := &moneyCaptureClient{}
	handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/wallet/money/parse", gateway.InternalUserIdentityMiddleware(), handler.ParseMoney)
	app.Post("/wallet/money/format", gateway.InternalUserIdentityMiddleware(), handler.FormatMoney)

	tests := []struct {
		path string
		body string
	}{
		{
			path: "/wallet/money/parse",
			body: `{"currency_code":"USD","currency_unit_version_id":"9007199254740993","major_units":"1.00","rounding_mode":"half_even"}`,
		},
		{
			path: "/wallet/money/format",
			body: `{"currency_code":"USD","currency_unit_version_id":"9007199254740994","minor_units":"100","rounding_mode":"half_even"}`,
		},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		setWalletPrincipalHeaders(request, walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour)))
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		closeWalletResponseBody(t, response.Body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", test.path, response.StatusCode, http.StatusOK)
		}
	}
	if client.parseRequest == nil || client.parseRequest.GetCurrencyUnitVersionId() != 9_007_199_254_740_993 {
		t.Fatalf("parse request = %+v, want exact unit ID", client.parseRequest)
	}
	if client.formatRequest == nil || client.formatRequest.GetCurrencyUnitVersionId() != 9_007_199_254_740_994 {
		t.Fatalf("format request = %+v, want exact unit ID", client.formatRequest)
	}
}

func TestQuoteConversionHandlerRejectsNumericOrMissingCurrencyUnitIDs(t *testing.T) {
	for _, body := range []string{
		`{"idempotency_key":"quote-invalid-units-1","source_code":"ecb-reference","base_currency":"EUR","base_currency_unit_version_id":11,"quote_currency":"USD","quote_currency_unit_version_id":"12","input_minor_units":"100","side":"mid"}`,
		`{"idempotency_key":"quote-invalid-units-2","source_code":"ecb-reference","base_currency":"EUR","quote_currency":"USD","quote_currency_unit_version_id":"12","input_minor_units":"100","side":"mid"}`,
	} {
		client := &moneyCaptureClient{}
		handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		app.Post("/wallet/fx/quotes", handler.QuoteConversion)
		request := httptest.NewRequest(http.MethodPost, "/wallet/fx/quotes", bytes.NewBufferString(body))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		defer closeWalletResponseBody(t, response.Body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d for %s", response.StatusCode, http.StatusBadRequest, body)
		}
		if client.quoteRequest != nil {
			t.Fatalf("client received invalid request: %+v", client.quoteRequest)
		}
	}
}

func TestMoneyHandlersForwardSourceDiscoveryAndUseSafeJSON(t *testing.T) {
	client := &moneyCaptureClient{}
	handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/wallet/fx/sources", gateway.InternalUserIdentityMiddleware(), handler.ListFXSources)

	request := httptest.NewRequest(http.MethodGet, "/wallet/fx/sources", nil)
	setWalletPrincipalHeaders(request, walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour)))
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWalletResponseBody(t, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if client.sourceRequest == nil || client.sourceRequest.GetTenantId() != "tenant-1" {
		t.Fatalf("source request = %+v, want authenticated tenant", client.sourceRequest)
	}
	payload := decodeJSONObject(t, response.Body)
	sources, ok := payload["sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Fatalf("sources = %#v", payload["sources"])
	}
	pairs := sources[0].(map[string]any)["pairs"].([]any)
	if pairID := pairs[0].(map[string]any)["id"]; pairID != "9007199254740993" {
		t.Fatalf("pair id = %#v, want exact string", pairID)
	}
}

func decodeJSONObject(t testing.TB, body io.Reader) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

type moneyCaptureClient struct {
	walletv1.WalletPublicServiceClient
	parseRequest  *walletv1.ParseMoneyPublicRequest
	formatRequest *walletv1.FormatMoneyPublicRequest
	quoteRequest  *walletv1.QuoteConversionPublicRequest
	sourceRequest *walletv1.ListFXSourcesPublicRequest
}

func (c *moneyCaptureClient) ParseMoneyPublic(
	_ context.Context,
	request *walletv1.ParseMoneyPublicRequest,
	_ ...grpc.CallOption,
) (*walletv1.ParseMoneyPublicResponse, error) {
	c.parseRequest = proto.Clone(request).(*walletv1.ParseMoneyPublicRequest)
	return &walletv1.ParseMoneyPublicResponse{}, nil
}

func (c *moneyCaptureClient) FormatMoneyPublic(
	_ context.Context,
	request *walletv1.FormatMoneyPublicRequest,
	_ ...grpc.CallOption,
) (*walletv1.FormatMoneyPublicResponse, error) {
	c.formatRequest = proto.Clone(request).(*walletv1.FormatMoneyPublicRequest)
	return &walletv1.FormatMoneyPublicResponse{}, nil
}

func (c *moneyCaptureClient) QuoteConversionPublic(
	_ context.Context,
	request *walletv1.QuoteConversionPublicRequest,
	_ ...grpc.CallOption,
) (*walletv1.QuoteConversionPublicResponse, error) {
	c.quoteRequest = proto.Clone(request).(*walletv1.QuoteConversionPublicRequest)
	return &walletv1.QuoteConversionPublicResponse{}, nil
}

func (c *moneyCaptureClient) ListFXSourcesPublic(
	_ context.Context,
	request *walletv1.ListFXSourcesPublicRequest,
	_ ...grpc.CallOption,
) (*walletv1.ListFXSourcesPublicResponse, error) {
	c.sourceRequest = proto.Clone(request).(*walletv1.ListFXSourcesPublicRequest)
	return &walletv1.ListFXSourcesPublicResponse{Sources: []*walletv1.FXSource{{
		Code: "ecb-reference",
		Pairs: []*walletv1.FXSourcePair{{
			Id:            9_007_199_254_740_993,
			BaseCurrency:  "EUR",
			QuoteCurrency: "USD",
			Sides:         []string{"mid"},
		}},
	}}}, nil
}
