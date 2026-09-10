package interop

import (
	"context"
	"encoding/json"
	"strconv"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

func CreateQuote(ctx context.Context, store *walletstore.Store, tenant, owner string, request *walletv1.CreateInteropQuoteRequest) (*walletstore.InteropQuote, error) {
	if request == nil || owner == "" {
		return nil, walletstore.ErrInteropInvalid
	}
	if !msisdn.MatchString(request.Recipient) || request.Currency != "SDG" {
		return nil, walletstore.ErrInteropInvalid
	}
	id, err := uuid.Parse(request.WalletId)
	if err != nil {
		return nil, walletstore.ErrMissingWalletID
	}
	amount, err := ParseMinor(request.AmountMinor)
	if err != nil {
		return nil, err
	}
	unit, err := strconv.ParseInt(request.CurrencyUnitVersion, 10, 64)
	if err != nil || unit <= 0 {
		return nil, walletstore.ErrInvalidCurrencyUnitID
	}
	binding, err := store.GetInteropBinding(ctx, tenant)
	if err != nil {
		return nil, err
	}
	alias, err := store.GetInteropAlias(ctx, tenant, id, "")
	if err != nil {
		return nil, err
	}
	intent := QuoteIntent{From: Party{IDType: "MSISDN", IDValue: alias.Identifier, FSPID: binding.FSPID, DisplayName: alias.DisplayName}, To: Party{IDType: "MSISDN", IDValue: request.Recipient}, Amount: Decimal(amount), Currency: request.Currency, AmountType: "SEND", TransactionType: "TRANSFER"}
	body, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	return store.CreateInteropQuote(ctx, walletstore.InteropQuote{ID: uuid.New(), TransferID: uuid.New(), TenantID: tenant, OwnerID: owner, WalletID: id, Direction: "OUT", IdempotencyKey: request.IdempotencyKey, Amount: amount, Currency: request.Currency, CurrencyUnitID: unit, Request: body, Status: "REQUESTED"})
}
