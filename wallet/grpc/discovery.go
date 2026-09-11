package walletgrpc

import (
	"context"
	"errors"
	"slices"
	"strconv"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetWalletAccount is a read-only projection of the authenticated NoEBS account.
// Identity providers and payment providers never create separate account owners.
func (s *Server) GetWalletAccount(ctx context.Context, req *walletv1.GetWalletAccountRequest) (*walletv1.WalletAccount, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}
	userID, err := strconv.ParseInt(owner, 10, 64)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid user identity")
	}
	wallets, err := s.Service.Store.ListUserWallets(ctx, tenant, userID)
	if err != nil {
		return nil, mapError(err)
	}
	response := &walletv1.WalletAccount{TenantId: tenant, UserId: owner, Wallets: make([]*walletv1.Wallet, 0, len(wallets))}
	for i := range wallets {
		w, err := s.walletProto(ctx, &wallets[i])
		if err != nil {
			return nil, mapError(err)
		}
		response.Wallets = append(response.Wallets, w)
	}
	return response, nil
}

// ListWalletProviders describes configured payment capabilities, not live
// availability or an assurance that any particular monetary command is eligible.
// Transfer and deposit admission remain authoritative in their existing services.
func (s *Server) ListWalletProviders(ctx context.Context, req *walletv1.ListWalletProvidersRequest) (*walletv1.ListWalletProvidersResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, _, err := s.walletIdentity(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}
	providers := map[string]*walletv1.WalletProvider{}
	for _, direction := range []string{"deposit", "withdrawal"} {
		methods, err := s.paymentMethods(ctx, walletstore.PSPMethodFilter{TenantID: tenant, Direction: direction})
		if err != nil {
			return nil, mapError(err)
		}
		for _, method := range methods {
			p := providers[method.ProviderCode]
			if p == nil {
				p = &walletv1.WalletProvider{Id: method.ProviderCode, Name: method.ProviderName, Available: true, Capabilities: &walletv1.WalletProviderCapabilities{}}
				providers[p.Id] = p
			}
			for _, currency := range method.Currencies {
				if !slices.Contains(p.Currencies, currency) {
					p.Currencies = append(p.Currencies, currency)
				}
			}
			if direction == "deposit" {
				p.Capabilities.Receive, p.Capabilities.Funding, p.FundingMode = true, true, "deposit"
			} else {
				p.Capabilities.Send, p.TransferMode = true, "withdrawal"
			}
		}
	}
	binding, err := s.Service.Store.GetInteropBinding(ctx, tenant)
	if err != nil && !errors.Is(err, walletstore.ErrInteropDisabled) {
		return nil, interopError(err)
	}
	if binding != nil {
		if _, exists := providers["mojaloop"]; exists {
			return nil, status.Error(codes.FailedPrecondition, "duplicate payment provider identity")
		}
		providers["mojaloop"] = &walletv1.WalletProvider{
			Id: "mojaloop", Name: "Mojaloop", Available: binding.Enabled,
			Capabilities: &walletv1.WalletProviderCapabilities{Send: binding.Enabled, Receive: binding.Enabled, Funding: binding.Enabled},
			Currencies:   []string{binding.Currency}, TransferMode: "interop_quote", FundingMode: "external_transfer",
		}
	}
	response := &walletv1.ListWalletProvidersResponse{Providers: make([]*walletv1.WalletProvider, 0, len(providers))}
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		slices.Sort(providers[id].Currencies)
		response.Providers = append(response.Providers, providers[id])
	}
	return response, nil
}

// ListWalletFundingMethods reveals receiving instructions only after checking
// the authenticated customer's ownership. It never registers an invented alias,
// creates a deposit, or treats a participant's switch position as customer funds.
func (s *Server) ListWalletFundingMethods(ctx context.Context, req *walletv1.ListWalletFundingMethodsRequest) (*walletv1.ListWalletFundingMethodsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, req.TenantId)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.WalletId)
	if err != nil || id == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	w, err := s.Service.Store.GetWallet(ctx, tenant, id)
	if err != nil {
		return nil, mapError(err)
	}
	if w.OwnerType != walletstore.OwnerTypeUser || w.OwnerID != owner || (w.UserID.Valid && strconv.FormatInt(w.UserID.Int64, 10) != owner) {
		return nil, status.Error(codes.NotFound, walletstore.ErrWalletNotFound.Error())
	}
	methods, err := s.paymentMethods(ctx, walletstore.PSPMethodFilter{TenantID: tenant, Direction: "deposit", Currency: w.Currency, CurrencyUnitID: w.CurrencyUnitID})
	if err != nil {
		return nil, mapError(err)
	}
	response := &walletv1.ListWalletFundingMethodsResponse{Methods: make([]*walletv1.WalletFundingMethod, 0, len(methods)+1)}
	for _, method := range methods {
		label := method.DisplayName
		if label == "" {
			label = method.ProviderName
		}
		m := &walletv1.WalletFundingMethod{
			Id: method.ProviderCode + ":deposit", ProviderId: method.ProviderCode, Label: label,
			Instructions: "Continue to enter your funding details.", Currency: w.Currency,
			CurrencyUnitVersion: strconv.FormatInt(w.CurrencyUnitID, 10), Available: w.Status == walletstore.WalletStatusActive,
			Mode: "deposit", InputSchemaJson: string(method.InputSchema),
		}
		if !m.Available {
			m.UnavailableReason = "wallet_inactive"
		}
		response.Methods = append(response.Methods, m)
	}
	binding, err := s.Service.Store.GetInteropBinding(ctx, tenant)
	if err != nil && !errors.Is(err, walletstore.ErrInteropDisabled) {
		return nil, interopError(err)
	}
	if binding == nil || binding.Currency != w.Currency || binding.CurrencyUnitID != w.CurrencyUnitID {
		return response, nil
	}
	m := &walletv1.WalletFundingMethod{
		Id: "mojaloop:receive", ProviderId: "mojaloop", Label: "Bank or wallet transfer",
		Currency: w.Currency, CurrencyUnitVersion: strconv.FormatInt(w.CurrencyUnitID, 10), Mode: "external_transfer",
	}
	switch {
	case !binding.Enabled:
		m.UnavailableReason, m.Instructions = "provider_unavailable", "Bank and wallet transfers are currently unavailable."
	case w.Status != walletstore.WalletStatusActive:
		m.UnavailableReason, m.Instructions = "wallet_inactive", "Your wallet must be active to receive a transfer."
	default:
		alias, err := s.Service.Store.GetInteropAlias(ctx, tenant, id, "")
		if errors.Is(err, walletstore.ErrInteropNotFound) {
			m.UnavailableReason, m.Instructions = "registration_required", "Contact support to register your receiving number."
		} else if err != nil {
			return nil, interopError(err)
		} else {
			m.Available, m.AccountIdentifier, m.AccountName = true, alias.Identifier, alias.DisplayName
			m.Instructions = "From a participating bank or wallet, send to this registered number. Your NoEBS balance updates when the transfer completes."
		}
	}
	response.Methods = append(response.Methods, m)
	return response, nil
}

// Pagination belongs at this API boundary; stores receive explicit limits and
// no configured method is silently dropped from the directory.
func (s *Server) paymentMethods(ctx context.Context, filter walletstore.PSPMethodFilter) ([]walletstore.PSPPaymentMethod, error) {
	const pageSize = 100
	filter.Limit, filter.Offset = pageSize, 0
	methods := make([]walletstore.PSPPaymentMethod, 0)
	for {
		page, err := s.Service.Store.ListAvailablePSPMethods(ctx, filter)
		if err != nil {
			return nil, err
		}
		methods = append(methods, page...)
		if len(page) < pageSize {
			return methods, nil
		}
		filter.Offset += len(page)
	}
}
