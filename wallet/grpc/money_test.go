package walletgrpc

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/groosh"
	"github.com/adonese/noebs/wallet"
	walletmoney "github.com/adonese/noebs/wallet/money"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestQuoteConversionRequestDoesNotExposeRoundingPolicy(t *testing.T) {
	fields := (&walletv1.QuoteConversionPublicRequest{}).ProtoReflect().Descriptor().Fields()
	if field := fields.ByName(protoreflect.Name("rounding_mode")); field != nil {
		t.Fatalf("rounding_mode is a client-controlled quote field: %v", field)
	}
}

func TestMoneyRequestsExposePinnedCurrencyUnitFields(t *testing.T) {
	tests := []struct {
		message protoreflect.MessageDescriptor
		fields  []protoreflect.Name
	}{
		{
			message: (&walletv1.ParseMoneyPublicRequest{}).ProtoReflect().Descriptor(),
			fields:  []protoreflect.Name{"currency_unit_version_id"},
		},
		{
			message: (&walletv1.FormatMoneyPublicRequest{}).ProtoReflect().Descriptor(),
			fields:  []protoreflect.Name{"currency_unit_version_id"},
		},
		{
			message: (&walletv1.QuoteConversionPublicRequest{}).ProtoReflect().Descriptor(),
			fields: []protoreflect.Name{
				"base_currency_unit_version_id", "quote_currency_unit_version_id",
			},
		},
	}
	for _, test := range tests {
		for _, name := range test.fields {
			field := test.message.Fields().ByName(name)
			if field == nil || field.Kind() != protoreflect.Int64Kind {
				t.Fatalf("%s.%s = %v, want int64 field", test.message.FullName(), name, field)
			}
		}
	}
}

func TestPaymentMethodProtoCarriesPinnedCurrencyUnit(t *testing.T) {
	const unitID int64 = 9_007_199_254_740_993
	currency := testCurrency(t, unitID, "USD", 2)
	method, err := paymentMethodProto(walletstore.PSPPaymentMethod{
		ProviderCode:   "test-psp",
		Currencies:     []string{"USD"},
		CurrencyUnitID: unitID,
		MinAmount:      sql.NullInt64{Int64: unitID, Valid: true},
		MaxAmount:      sql.NullInt64{Int64: unitID + 1, Valid: true},
	}, &currency)
	if err != nil {
		t.Fatalf("paymentMethodProto() error = %v", err)
	}
	if method.GetCurrencyUnitVersionId() != 9_007_199_254_740_993 {
		t.Fatalf("payment method unit ID = %d, want 9007199254740993", method.GetCurrencyUnitVersionId())
	}
	if method.GetMinAmountMoney().GetMinorUnits() != "9007199254740993" ||
		method.GetMaxAmountMoney().GetMinorUnits() != "9007199254740994" {
		t.Fatalf("payment method exact bounds = %v/%v", method.GetMinAmountMoney(), method.GetMaxAmountMoney())
	}
	if method.GetMinAmountMoney().GetCurrencyUnitVersionId() != unitID ||
		method.GetMinAmountMoney().GetCurrencyCode() != "USD" {
		t.Fatalf("payment method exact minimum identity = %v", method.GetMinAmountMoney())
	}
}

func TestPaymentMethodProtoRejectsBoundsWithoutOneExactCurrencyIdentity(t *testing.T) {
	currency := testCurrency(t, 101, "USD", 2)
	method := walletstore.PSPPaymentMethod{
		ProviderCode:   "test-psp",
		Currencies:     []string{"USD"},
		CurrencyUnitID: currency.Definition.ID,
		MinAmount:      sql.NullInt64{Int64: 100, Valid: true},
	}
	if _, err := paymentMethodProto(method, nil); !errors.Is(err, walletmoney.ErrInvalidCurrencyUnitData) {
		t.Fatalf("unbound payment bounds error = %v, want %v", err, walletmoney.ErrInvalidCurrencyUnitData)
	}
	mismatched := currency
	mismatched.Definition.ID++
	if _, err := paymentMethodProto(method, &mismatched); !errors.Is(err, walletmoney.ErrInvalidCurrencyUnitData) {
		t.Fatalf("mismatched payment unit error = %v, want %v", err, walletmoney.ErrInvalidCurrencyUnitData)
	}
	ambiguous := method
	ambiguous.Currencies = []string{"USD", "AED"}
	if _, err := paymentMethodProto(ambiguous, &currency); !errors.Is(err, walletmoney.ErrInvalidCurrencyUnitData) {
		t.Fatalf("ambiguous payment currency error = %v, want %v", err, walletmoney.ErrInvalidCurrencyUnitData)
	}
}

func TestPublicMoneyRPCsRejectMissingPinnedCurrencyUnitsBeforeStoreAccess(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	ctx := metadata.NewIncomingContext(context.Background(), userMetadata(42, "tenant-a"))
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "parse",
			call: func() error {
				_, err := server.ParseMoneyPublic(ctx, &walletv1.ParseMoneyPublicRequest{
					TenantId: "tenant-a", CurrencyCode: "USD", MajorUnits: "1.00", RoundingMode: "half_even",
				})
				return err
			},
		},
		{
			name: "format",
			call: func() error {
				_, err := server.FormatMoneyPublic(ctx, &walletv1.FormatMoneyPublicRequest{
					TenantId: "tenant-a", CurrencyCode: "USD", MinorUnits: "100", RoundingMode: "half_even",
				})
				return err
			},
		},
		{
			name: "quote",
			call: func() error {
				_, err := server.QuoteConversionPublic(ctx, &walletv1.QuoteConversionPublicRequest{
					TenantId: "tenant-a", UserId: 42, SourceCode: "ecb-reference", BaseCurrency: "EUR",
					QuoteCurrency: "USD", InputMinorUnits: "100", Side: walletstore.FXSideMid,
					IdempotencyKey: "quote-request-1",
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrMissingCurrencyUnitID.Error() {
				t.Fatalf("error = %v, want InvalidArgument %q", err, walletstore.ErrMissingCurrencyUnitID)
			}
		})
	}
}

func TestValidatePublicCurrencyUnitIDUsesTypedErrors(t *testing.T) {
	if err := validatePublicCurrencyUnitID(0); !errors.Is(err, walletstore.ErrMissingCurrencyUnitID) {
		t.Fatalf("zero unit error = %v, want %v", err, walletstore.ErrMissingCurrencyUnitID)
	}
	if err := validatePublicCurrencyUnitID(-1); !errors.Is(err, walletstore.ErrInvalidCurrencyUnitID) {
		t.Fatalf("negative unit error = %v, want %v", err, walletstore.ErrInvalidCurrencyUnitID)
	}
	if err := validatePublicCurrencyUnitID(1); err != nil {
		t.Fatalf("positive unit error = %v", err)
	}
}

func TestConversionQuoteProtoSeparatesRequestedAndObservedSides(t *testing.T) {
	usd := testCurrency(t, 101, "USD", 2)
	aed := testCurrency(t, 202, "AED", 2)
	input, err := groosh.NewMoney(12345, usd.Unit)
	if err != nil {
		t.Fatal(err)
	}
	output, err := groosh.NewMoney(40000, aed.Unit)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	quote := walletmoney.ConversionQuote{
		Quote: walletstore.MoneyConversionQuote{
			ID:             uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			IdempotencyKey: "quote-request-1",
			Inverse:        true,
			RoundingMode:   groosh.RoundHalfEven.String(),
			ConversionAt:   now,
			ExpiresAt:      now.Add(time.Hour),
		},
		Input:  input,
		Output: output,
		Observation: walletstore.FXObservation{
			ID:                9_007_199_254_740_993,
			BaseCurrencyCode:  "AED",
			QuoteCurrencyCode: "USD",
			Rate:              decimal.RequireFromString("0.3075"),
			Side:              walletstore.FXSideAsk,
			ObservationAt:     now,
			RetrievedAt:       now.Add(time.Minute),
			SourceRevision:    "2026-07-21",
		},
		Source: walletstore.FXSource{
			Code:        "cbuae-reference",
			DisplayName: "CBUAE reference",
			Purpose:     walletstore.FXPurposeReference,
			SourceURL:   "https://www.centralbank.ae/",
		},
		AppliedRate: big.NewRat(40000, 12345),
	}

	got, err := conversionQuoteProto(quote)
	if err != nil {
		t.Fatalf("conversionQuoteProto() error = %v", err)
	}
	if got.GetRequestedRateSide() != walletstore.FXSideBid {
		t.Fatalf("requested side = %q, want %q", got.GetRequestedRateSide(), walletstore.FXSideBid)
	}
	if got.GetObservationRateSide() != walletstore.FXSideAsk {
		t.Fatalf("observation side = %q, want %q", got.GetObservationRateSide(), walletstore.FXSideAsk)
	}
	if got.GetExecutable() {
		t.Fatal("reference conversion quote is marked executable")
	}
	executableField := got.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name("executable"))
	if !got.ProtoReflect().Has(executableField) {
		t.Fatal("reference conversion quote omits explicit executable=false presence")
	}
	encoded, err := protojson.Marshal(got)
	if err != nil {
		t.Fatalf("protojson.Marshal() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"executable":false`)) {
		t.Fatalf("quote JSON omits explicit executable=false: %s", encoded)
	}
	if got.GetInput().GetCurrencyUnitVersionId() != 101 || got.GetOutput().GetCurrencyUnitVersionId() != 202 {
		t.Fatalf("quote unit versions = %d/%d, want 101/202", got.GetInput().GetCurrencyUnitVersionId(), got.GetOutput().GetCurrencyUnitVersionId())
	}
	if got.GetConversionAt() != now.Format(time.RFC3339Nano) {
		t.Fatalf("conversion_at = %q, want %q", got.GetConversionAt(), now.Format(time.RFC3339Nano))
	}
	if got.GetIdempotencyKey() != "quote-request-1" {
		t.Fatalf("idempotency key = %q", got.GetIdempotencyKey())
	}
}

func TestWalletLedgerEntryProtoUsesPinnedCurrencyUnit(t *testing.T) {
	currency := testCurrency(t, 303, "USD", 2)
	entry := walletstore.WalletLedgerEntry{
		ID:             1,
		TransactionID:  2,
		WalletID:       uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		EntryType:      "credit",
		Amount:         9_007_199_254_740_993,
		Currency:       "USD",
		CurrencyUnitID: 303,
		BalanceAfter:   9_007_199_254_740_994,
		WalletSequence: 1,
		CreatedAt:      time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
	}

	got, err := walletLedgerEntryProto(entry, currency)
	if err != nil {
		t.Fatalf("walletLedgerEntryProto() error = %v", err)
	}
	if got.GetAmountMoney().GetMinorUnits() != "9007199254740993" || got.GetBalanceAfterMoney().GetMinorUnits() != "9007199254740994" {
		t.Fatalf("exact money fields = %q/%q", got.GetAmountMoney().GetMinorUnits(), got.GetBalanceAfterMoney().GetMinorUnits())
	}
	if got.GetAmountMoney().GetCurrencyUnitVersionId() != 303 {
		t.Fatalf("currency unit version = %d, want 303", got.GetAmountMoney().GetCurrencyUnitVersionId())
	}

	entry.CurrencyUnitID++
	if _, err := walletLedgerEntryProto(entry, currency); err == nil {
		t.Fatal("walletLedgerEntryProto() accepted mismatched pinned unit")
	}
}

func TestWalletProtoUsesPinnedCurrencyUnit(t *testing.T) {
	currency := testCurrency(t, 404, "JPY", 0)
	wallet := &walletstore.Wallet{
		ID:               uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Currency:         "JPY",
		CurrencyUnitID:   404,
		Balance:          9_007_199_254_740_993,
		AvailableBalance: 9_007_199_254_740_992,
	}

	got, err := walletProtoWithCurrency(wallet, currency)
	if err != nil {
		t.Fatalf("walletProtoWithCurrency() error = %v", err)
	}
	if got.GetBalanceMoney().GetMinorUnits() != "9007199254740993" || got.GetBalanceMoney().GetMajorUnits() != "9007199254740993" {
		t.Fatalf("balance money = %+v, want exact zero-exponent JPY", got.GetBalanceMoney())
	}
	if got.GetAvailableBalanceMoney().GetCurrencyUnitVersionId() != 404 {
		t.Fatalf("available balance unit version = %d, want 404", got.GetAvailableBalanceMoney().GetCurrencyUnitVersionId())
	}

	wallet.CurrencyUnitID++
	if _, err := walletProtoWithCurrency(wallet, currency); err == nil {
		t.Fatal("walletProtoWithCurrency() accepted mismatched pinned unit")
	}
}

func TestFXSourceProtoPublishesOnlyConfiguredPairSides(t *testing.T) {
	got := fxSourceProto(
		walletstore.FXSource{
			Code:          "ecb-reference",
			DisplayName:   "ECB reference",
			Purpose:       walletstore.FXPurposeReference,
			SourceURL:     "https://data-api.ecb.europa.eu/",
			MaxAgeSeconds: 604800,
		},
		[]walletstore.FXSourcePair{
			{ID: 11, BaseCurrencyCode: "EUR", QuoteCurrencyCode: "USD"},
			{ID: 12, BaseCurrencyCode: "EUR", QuoteCurrencyCode: "GBP"},
		},
		map[int64][]string{
			11: {walletstore.FXSideMid},
			12: {walletstore.FXSideBid, walletstore.FXSideAsk},
		},
	)
	if got.GetCode() != "ecb-reference" || len(got.GetPairs()) != 2 {
		t.Fatalf("source = %+v", got)
	}
	if sides := got.GetPairs()[0].GetSides(); len(sides) != 1 || sides[0] != walletstore.FXSideMid {
		t.Fatalf("first pair sides = %v, want [mid]", sides)
	}
	if sides := got.GetPairs()[1].GetSides(); len(sides) != 2 || sides[0] != walletstore.FXSideBid || sides[1] != walletstore.FXSideAsk {
		t.Fatalf("second pair sides = %v, want [bid ask]", sides)
	}
}

func testCurrency(t testing.TB, versionID int64, code string, exponent uint8) walletmoney.Currency {
	t.Helper()
	effectiveFrom := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        versionID,
		Code:             code,
		ISOMinorExponent: &exponent,
		DisplayExponent:  &exponent,
		CashExponent:     &exponent,
		CashIncrement:    1,
		EffectiveFrom:    effectiveFrom,
	})
	if err != nil {
		t.Fatalf("NewCurrencyUnit() error = %v", err)
	}
	return walletmoney.Currency{
		Definition: walletstore.CurrencyUnitVersion{
			ID:           versionID,
			CurrencyCode: code,
			ValidFrom:    effectiveFrom,
		},
		Unit: unit,
	}
}
