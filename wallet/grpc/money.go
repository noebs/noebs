package walletgrpc

import (
	"context"
	"strconv"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/groosh"
	"github.com/adonese/noebs/wallet"
	walletmoney "github.com/adonese/noebs/wallet/money"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxPublicMoneyStringLength = 128

func (s *Server) ListCurrenciesPublic(ctx context.Context, req *walletv1.ListCurrenciesPublicRequest) (*walletv1.ListCurrenciesPublicResponse, error) {
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if _, _, err := s.publicMoneyClaims(ctx, req.TenantId); err != nil {
		return nil, err
	}
	currencies, err := walletmoney.NewService(s.Service.Store).ListCurrencies(ctx, time.Now().UTC(), true)
	if err != nil {
		return nil, mapError(err)
	}
	response := make([]*walletv1.CurrencyUnit, 0, len(currencies))
	for _, currency := range currencies {
		response = append(response, currencyUnitProto(currency))
	}
	return &walletv1.ListCurrenciesPublicResponse{Currencies: response}, nil
}

func (s *Server) GetCurrencyPublic(ctx context.Context, req *walletv1.GetCurrencyPublicRequest) (*walletv1.GetCurrencyPublicResponse, error) {
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if _, _, err := s.publicMoneyClaims(ctx, req.TenantId); err != nil {
		return nil, err
	}
	currency, err := walletmoney.NewService(s.Service.Store).GetCurrency(ctx, req.CurrencyCode, time.Now().UTC(), true)
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.GetCurrencyPublicResponse{Currency: currencyUnitProto(currency)}, nil
}

func (s *Server) ParseMoneyPublic(ctx context.Context, req *walletv1.ParseMoneyPublicRequest) (*walletv1.ParseMoneyPublicResponse, error) {
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if _, _, err := s.publicMoneyClaims(ctx, req.TenantId); err != nil {
		return nil, err
	}
	if req.MajorUnits == "" || len(req.MajorUnits) > maxPublicMoneyStringLength {
		return nil, status.Error(codes.InvalidArgument, groosh.ErrInvalidAmountSyntax.Error())
	}
	if err := validatePublicCurrencyUnitID(req.CurrencyUnitVersionId); err != nil {
		return nil, mapError(err)
	}
	mode, err := groosh.ParseRoundingMode(req.RoundingMode)
	if err != nil {
		return nil, mapError(err)
	}
	amount, err := walletmoney.NewService(s.Service.Store).ParseMajor(
		ctx, req.CurrencyCode, req.CurrencyUnitVersionId, req.MajorUnits, time.Now().UTC(),
	)
	if err != nil {
		return nil, mapError(err)
	}
	response, err := moneyAmountProto(amount, mode)
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.ParseMoneyPublicResponse{Money: response}, nil
}

func (s *Server) FormatMoneyPublic(ctx context.Context, req *walletv1.FormatMoneyPublicRequest) (*walletv1.FormatMoneyPublicResponse, error) {
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if _, _, err := s.publicMoneyClaims(ctx, req.TenantId); err != nil {
		return nil, err
	}
	minor, err := parseCanonicalMinorUnits(req.MinorUnits)
	if err != nil {
		return nil, err
	}
	mode, err := groosh.ParseRoundingMode(req.RoundingMode)
	if err != nil {
		return nil, mapError(err)
	}
	if err := validatePublicCurrencyUnitID(req.CurrencyUnitVersionId); err != nil {
		return nil, mapError(err)
	}
	amount, _, err := walletmoney.NewService(s.Service.Store).FormatMinor(
		ctx, req.CurrencyCode, req.CurrencyUnitVersionId, minor, mode,
	)
	if err != nil {
		return nil, mapError(err)
	}
	response, err := moneyAmountProto(amount, mode)
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.FormatMoneyPublicResponse{Money: response}, nil
}

func (s *Server) QuoteConversionPublic(ctx context.Context, req *walletv1.QuoteConversionPublicRequest) (*walletv1.QuoteConversionPublicResponse, error) {
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	claims, tenantID, err := s.publicMoneyClaims(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}
	userID, err := bindUserIDToClaims(req.UserId, claims)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
	}
	if err := walletstore.ValidateIdempotencyKey(req.IdempotencyKey); err != nil {
		return nil, mapError(err)
	}
	if err := validatePublicCurrencyUnitID(req.BaseCurrencyUnitVersionId); err != nil {
		return nil, mapError(err)
	}
	if err := validatePublicCurrencyUnitID(req.QuoteCurrencyUnitVersionId); err != nil {
		return nil, mapError(err)
	}
	minor, err := parseCanonicalMinorUnits(req.InputMinorUnits)
	if err != nil {
		return nil, err
	}
	quote, err := walletmoney.NewService(s.Service.Store).QuoteConversion(ctx, walletmoney.QuoteParams{
		TenantID:                tenantID,
		RequestedBy:             userID,
		IdempotencyKey:          req.IdempotencyKey,
		MaxQuotesPerObservation: s.Service.Config.WalletFXQuoteMaxPerUserObservation,
		SourceCode:              req.SourceCode,
		BaseCurrency:            req.BaseCurrency,
		BaseCurrencyUnitID:      req.BaseCurrencyUnitVersionId,
		QuoteCurrency:           req.QuoteCurrency,
		QuoteCurrencyUnitID:     req.QuoteCurrencyUnitVersionId,
		InputMinor:              minor,
		Side:                    req.Side,
		RoundingMode:            groosh.RoundHalfEven,
		ConversionTime:          time.Now().UTC(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	response, err := conversionQuoteProto(quote)
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.QuoteConversionPublicResponse{Quote: response}, nil
}

func (s *Server) GetConversionQuotePublic(ctx context.Context, req *walletv1.GetConversionQuotePublicRequest) (*walletv1.GetConversionQuotePublicResponse, error) {
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	claims, tenantID, err := s.publicMoneyClaims(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}
	userID, err := bindUserIDToClaims(req.UserId, claims)
	if err != nil {
		return nil, err
	}
	quoteID, err := uuid.Parse(req.QuoteId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingConversionQuoteID.Error())
	}
	quote, err := walletmoney.NewService(s.Service.Store).GetConversionQuote(ctx, tenantID, userID, quoteID)
	if err != nil {
		return nil, mapError(err)
	}
	response, err := conversionQuoteProto(quote)
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.GetConversionQuotePublicResponse{Quote: response}, nil
}

func (s *Server) ListFXSourcesPublic(ctx context.Context, req *walletv1.ListFXSourcesPublicRequest) (*walletv1.ListFXSourcesPublicResponse, error) {
	if err := s.requirePublicWalletRPC(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if _, _, err := s.publicMoneyClaims(ctx, req.TenantId); err != nil {
		return nil, err
	}

	sources, err := s.Service.Store.ListEnabledFXSources(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	response := make([]*walletv1.FXSource, 0, len(sources))
	for _, source := range sources {
		pairs, err := s.Service.Store.ListEnabledFXSourcePairs(ctx, source.ID)
		if err != nil {
			return nil, mapError(err)
		}
		configuredSides, err := s.Service.Store.ListEnabledFXSourcePairSides(ctx, source.ID)
		if err != nil {
			return nil, mapError(err)
		}
		sidesByPair := make(map[int64][]string, len(pairs))
		for _, configured := range configuredSides {
			sidesByPair[configured.SourcePairID] = append(sidesByPair[configured.SourcePairID], configured.Side)
		}
		response = append(response, fxSourceProto(source, pairs, sidesByPair))
	}
	return &walletv1.ListFXSourcesPublicResponse{Sources: response}, nil
}

func fxSourceProto(source walletstore.FXSource, pairs []walletstore.FXSourcePair, sidesByPair map[int64][]string) *walletv1.FXSource {
	protoPairs := make([]*walletv1.FXSourcePair, 0, len(pairs))
	for _, pair := range pairs {
		protoPairs = append(protoPairs, &walletv1.FXSourcePair{
			Id:            pair.ID,
			BaseCurrency:  pair.BaseCurrencyCode,
			QuoteCurrency: pair.QuoteCurrencyCode,
			Sides:         sidesByPair[pair.ID],
		})
	}
	return &walletv1.FXSource{
		Code:          source.Code,
		DisplayName:   source.DisplayName,
		Purpose:       source.Purpose,
		SourceUrl:     source.SourceURL,
		MaxAgeSeconds: int32(source.MaxAgeSeconds),
		Pairs:         protoPairs,
	}
}

func (s *Server) publicMoneyClaims(ctx context.Context, requestedTenant string) (*gatewayPrincipal, string, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, "", status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	claims, err := s.requireGatewayClaims(ctx)
	if err != nil {
		return nil, "", err
	}
	tenantID, err := bindTenantToClaims(requestedTenant, claims)
	if err != nil {
		return nil, "", err
	}
	return (*gatewayPrincipal)(claims), tenantID, nil
}

// gatewayPrincipal is an alias kept local so money RPC helpers do not loosen
// the existing gateway identity contract.
type gatewayPrincipal = gateway.PrincipalIdentity

func parseCanonicalMinorUnits(value string) (int64, error) {
	if value == "" || len(value) > maxPublicMoneyStringLength {
		return 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	minor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(minor, 10) != value {
		return 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	return minor, nil
}

func validatePublicCurrencyUnitID(currencyUnitID int64) error {
	return walletstore.ValidateCurrencyUnitID(currencyUnitID)
}

func currencyUnitProto(currency walletmoney.Currency) *walletv1.CurrencyUnit {
	definition := currency.Definition
	response := &walletv1.CurrencyUnit{
		CurrencyCode:          definition.CurrencyCode,
		Name:                  definition.Name,
		Kind:                  definition.Kind,
		Active:                definition.IsActive,
		DisplayExponent:       uint32(currency.Unit.DisplayExponent()),
		CashExponent:          uint32(currency.Unit.CashExponent()),
		CashRoundingIncrement: strconv.FormatInt(currency.Unit.CashIncrement(), 10),
		VersionId:             currency.Unit.VersionID(),
		ValidFrom:             definition.ValidFrom.UTC().Format(time.DateOnly),
		Source:                definition.Source,
		SourceRevision:        definition.SourceRevision,
		SourcePublishedOn:     definition.SourcePublishedOn.UTC().Format(time.DateOnly),
	}
	if definition.NumericCode.Valid {
		response.NumericCode = &definition.NumericCode.String
	}
	if exponent, present := currency.Unit.ISOMinorExponent(); present {
		value := uint32(exponent)
		response.IsoMinorExponent = &value
	}
	if definition.ValidTo.Valid {
		value := definition.ValidTo.Time.UTC().Format(time.DateOnly)
		response.ValidTo = &value
	}
	return response
}

func moneyAmountProto(amount groosh.Money, mode groosh.RoundingMode) (*walletv1.MoneyAmount, error) {
	minor, err := amount.MinorString()
	if err != nil {
		return nil, err
	}
	major, err := amount.MajorString()
	if err != nil {
		return nil, err
	}
	display, err := amount.DisplayRounded(mode)
	if err != nil {
		return nil, err
	}
	canonical, err := amount.CanonicalString()
	if err != nil {
		return nil, err
	}
	exponent, present := amount.Unit().ISOMinorExponent()
	if !present {
		return nil, groosh.ErrMissingISOMinorExponent
	}
	return &walletv1.MoneyAmount{
		MinorUnits:            minor,
		CurrencyCode:          amount.CurrencyCode(),
		CurrencyUnitVersionId: amount.UnitVersionID(),
		MinorExponent:         uint32(exponent),
		MajorUnits:            major,
		Display:               display,
		Canonical:             canonical,
	}, nil
}

func conversionQuoteProto(quote walletmoney.ConversionQuote) (*walletv1.ConversionQuote, error) {
	if quote.AppliedRate == nil || quote.AppliedRate.Sign() <= 0 {
		return nil, groosh.ErrInvalidRate
	}
	if quote.Quote.ConversionAt.IsZero() {
		return nil, walletmoney.ErrQuoteIntegrity
	}
	mode, err := groosh.ParseRoundingMode(quote.Quote.RoundingMode)
	if err != nil {
		return nil, err
	}
	input, err := moneyAmountProto(quote.Input, mode)
	if err != nil {
		return nil, err
	}
	output, err := moneyAmountProto(quote.Output, mode)
	if err != nil {
		return nil, err
	}
	executable := false
	response := &walletv1.ConversionQuote{
		Id:                       quote.Quote.ID.String(),
		Input:                    input,
		Output:                   output,
		RoundingMode:             quote.Quote.RoundingMode,
		SourceCode:               quote.Source.Code,
		SourceName:               quote.Source.DisplayName,
		SourcePurpose:            quote.Source.Purpose,
		SourceUrl:                quote.Source.SourceURL,
		ObservationId:            quote.Observation.ID,
		ObservationBaseCurrency:  quote.Observation.BaseCurrencyCode,
		ObservationQuoteCurrency: quote.Observation.QuoteCurrencyCode,
		ObservationRateDecimal:   quote.Observation.Rate.String(),
		AppliedRateNumerator:     quote.AppliedRate.Num().String(),
		AppliedRateDenominator:   quote.AppliedRate.Denom().String(),
		RequestedRateSide:        requestedFXSide(quote.Observation.Side, quote.Quote.Inverse),
		Inverse:                  quote.Quote.Inverse,
		ObservationAt:            quote.Observation.ObservationAt.UTC().Format(time.RFC3339Nano),
		RetrievedAt:              quote.Observation.RetrievedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:                quote.Quote.ExpiresAt.UTC().Format(time.RFC3339Nano),
		SourceRevision:           quote.Observation.SourceRevision,
		Executable:               &executable,
		ObservationRateSide:      quote.Observation.Side,
		ConversionAt:             quote.Quote.ConversionAt.UTC().Format(time.RFC3339Nano),
		IdempotencyKey:           quote.Quote.IdempotencyKey,
	}
	if quote.Observation.PublishedAt.Valid {
		value := quote.Observation.PublishedAt.Time.UTC().Format(time.RFC3339Nano)
		response.PublishedAt = &value
	}
	return response, nil
}

func requestedFXSide(observationSide string, inverse bool) string {
	if !inverse {
		return observationSide
	}
	switch observationSide {
	case walletstore.FXSideBid:
		return walletstore.FXSideAsk
	case walletstore.FXSideAsk:
		return walletstore.FXSideBid
	default:
		return observationSide
	}
}
