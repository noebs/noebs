package interop

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type NativeQuote struct {
	Payer           NativeParty `json:"payer"`
	Payee           NativeParty `json:"payee"`
	QuoteID         string      `json:"quoteId"`
	TransactionID   string      `json:"transactionId"`
	Amount          Money       `json:"amount"`
	AmountType      string      `json:"amountType"`
	Expiration      string      `json:"expiration"`
	Fees            *Money      `json:"fees,omitempty"`
	TransactionType struct {
		Scenario      string `json:"scenario"`
		Initiator     string `json:"initiator"`
		InitiatorType string `json:"initiatorType"`
	} `json:"transactionType"`
}
type IncomingQuote struct {
	QuoteIntent
	QuoteID       string `json:"quoteId"`
	TransactionID string `json:"transactionId"`
	Expiration    string `json:"expiration"`
	Protocol      struct {
		Source  string          `json:"source"`
		Request json.RawMessage `json:"request"`
	} `json:"noebsProtocol"`
}
type IncomingPrepare struct {
	TransferID string `json:"transferId"`
	Protocol   struct {
		Source  string            `json:"source"`
		Headers map[string]string `json:"headers"`
		Prepare Prepare           `json:"prepare"`
		Quote   struct {
			Request    json.RawMessage `json:"request"`
			Response   QuoteResponse   `json:"mojaloopResponse"`
			Fulfilment string          `json:"fulfilment"`
		} `json:"quote"`
	} `json:"noebsProtocol"`
}
type BackendQuoteResponse struct {
	TransferAmount  string `json:"transferAmount"`
	Currency        string `json:"transferAmountCurrency"`
	ReceiveAmount   string `json:"payeeReceiveAmount"`
	ReceiveCurrency string `json:"payeeReceiveAmountCurrency"`
	Expiration      string `json:"expiration"`
}

func (w *Worker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /parties/{type}/{identifier}", w.party)
	mux.HandleFunc("POST /quoterequests", w.incomingQuote)
	mux.HandleFunc("POST /transfers", w.incomingPrepare)
	mux.HandleFunc("PUT /transfers/{id}", w.notification)
	mux.HandleFunc("GET /transfers/{id}", w.incomingStatus)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// This listener must also be bound literally to loopback. Checking the peer
		// rejects accidental exposure through future listener/config refactors.
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(rw, r.Body, 1<<20)
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		mux.ServeHTTP(rw, r.WithContext(ctx))
	})
}
func readJSON(r *http.Request, target any) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		return nil, ErrProtocol
	}
	if err = json.Unmarshal(body, target); err != nil {
		return nil, ErrProtocol
	}
	return body, nil
}
func writeJSON(rw http.ResponseWriter, code int, value any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(value)
}
func backendError(rw http.ResponseWriter, err error) {
	code := http.StatusServiceUnavailable
	description := "backend_unavailable"
	switch {
	case errors.Is(err, ErrProtocol), errors.Is(err, walletstore.ErrInteropInvalid):
		code = http.StatusBadRequest
		description = "invalid_transfer"
	case errors.Is(err, walletstore.ErrInteropNotFound), errors.Is(err, walletstore.ErrWalletNotFound):
		code = http.StatusNotFound
		description = "party_or_transfer_not_found"
	case errors.Is(err, walletstore.ErrInteropConflict):
		code = http.StatusConflict
		description = "conflicting_transfer"
	case errors.Is(err, walletstore.ErrInteropQuoteExpired), errors.Is(err, walletstore.ErrInteropDisabled), errors.Is(err, walletstore.ErrWalletInactive), errors.Is(err, walletstore.ErrTransactionLimitNotFound):
		code = http.StatusUnprocessableEntity
		description = "transfer_not_eligible"
	}
	writeJSON(rw, code, map[string]string{"message": description})
}
func (w *Worker) party(rw http.ResponseWriter, r *http.Request) {
	if r.PathValue("type") != "MSISDN" || !msisdn.MatchString(r.PathValue("identifier")) {
		backendError(rw, ErrProtocol)
		return
	}
	b, err := w.Store.GetInteropBinding(r.Context(), w.Tenant)
	if err != nil {
		backendError(rw, err)
		return
	}
	if !b.Enabled {
		backendError(rw, walletstore.ErrInteropDisabled)
		return
	}
	alias, err := w.Store.GetInteropAlias(r.Context(), w.Tenant, uuid.Nil, r.PathValue("identifier"))
	if err != nil {
		backendError(rw, err)
		return
	}
	wallet, err := w.Store.GetWallet(r.Context(), w.Tenant, alias.WalletID)
	if err != nil {
		backendError(rw, err)
		return
	}
	if wallet.Status != walletstore.WalletStatusActive || wallet.OwnerType != walletstore.OwnerTypeUser {
		backendError(rw, walletstore.ErrWalletInactive)
		return
	}
	writeJSON(rw, http.StatusOK, Party{IDType: "MSISDN", IDValue: alias.Identifier, FSPID: w.FSPID, DisplayName: alias.DisplayName})
}
func (w *Worker) incomingQuote(rw http.ResponseWriter, r *http.Request) {
	var request IncomingQuote
	raw, err := readJSON(r, &request)
	if err != nil {
		backendError(rw, err)
		return
	}
	var native NativeQuote
	if json.Unmarshal(request.Protocol.Request, &native) != nil || native.QuoteID != request.QuoteID || native.TransactionID != request.TransactionID || request.Protocol.Source != request.From.FSPID || request.From.FSPID == w.FSPID || request.From.FSPID == "" || request.To.FSPID != w.FSPID || request.To.IDType != "MSISDN" || !msisdn.MatchString(request.To.IDValue) || request.From.IDType != "MSISDN" || !msisdn.MatchString(request.From.IDValue) || request.Currency != "SDG" || request.TransactionType != "TRANSFER" || request.AmountType != "SEND" || native.AmountType != "SEND" || native.TransactionType.Scenario != "TRANSFER" || native.TransactionType.Initiator != "PAYER" || native.TransactionType.InitiatorType != "CONSUMER" || !zeroFee(native.Fees, "SDG") {
		backendError(rw, ErrProtocol)
		return
	}
	if !native.Payer.matches(request.From) || !native.Payee.matches(request.To) {
		backendError(rw, ErrProtocol)
		return
	}
	quoteID, err := uuid.Parse(request.QuoteID)
	if err != nil {
		backendError(rw, ErrProtocol)
		return
	}
	transferID, err := uuid.Parse(request.TransactionID)
	if err != nil {
		backendError(rw, ErrProtocol)
		return
	}
	amount, err := ParseDecimal(request.Amount)
	if err != nil || amount <= 0 || !moneyMatches(native.Amount, amount, request.Currency) {
		backendError(rw, ErrProtocol)
		return
	}
	expiry, err := time.Parse(time.RFC3339Nano, request.Expiration)
	if err != nil || native.Expiration != request.Expiration {
		backendError(rw, ErrProtocol)
		return
	}
	// Existing immutable quote retries recover their original response even when
	// expired. New admissions still require a live agreement.
	existing, existingErr := w.Store.GetInteropQuote(r.Context(), w.Tenant, quoteID)
	if existingErr != nil && !errors.Is(existingErr, walletstore.ErrInteropNotFound) {
		backendError(rw, existingErr)
		return
	}
	if existing == nil && !expiry.After(time.Now().Add(5*time.Second)) {
		backendError(rw, walletstore.ErrInteropQuoteExpired)
		return
	}
	alias, err := w.Store.GetInteropAlias(r.Context(), w.Tenant, uuid.Nil, request.To.IDValue)
	if err != nil {
		backendError(rw, err)
		return
	}
	wallet, err := w.Store.GetWallet(r.Context(), w.Tenant, alias.WalletID)
	if err != nil {
		backendError(rw, err)
		return
	}
	response, err := json.Marshal(BackendQuoteResponse{TransferAmount: Decimal(amount), Currency: "SDG", ReceiveAmount: Decimal(amount), ReceiveCurrency: "SDG", Expiration: expiry.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		backendError(rw, err)
		return
	}
	q, err := w.Store.CreateInteropQuote(r.Context(), walletstore.InteropQuote{ID: quoteID, TransferID: transferID, TenantID: w.Tenant, OwnerID: wallet.OwnerID, WalletID: wallet.ID, Direction: "IN", IdempotencyKey: "incoming:" + quoteID.String(), Amount: amount, Currency: "SDG", CurrencyUnitID: wallet.CurrencyUnitID, Request: raw, Response: response, Status: "READY", ExpiresAt: sql.NullTime{Time: expiry, Valid: true}})
	if err != nil {
		backendError(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, json.RawMessage(q.Response))
}
func (w *Worker) incomingPrepare(rw http.ResponseWriter, r *http.Request) {
	var request IncomingPrepare
	raw, err := readJSON(r, &request)
	if err != nil {
		backendError(rw, err)
		return
	}
	id, err := uuid.Parse(request.TransferID)
	if err != nil {
		backendError(rw, ErrProtocol)
		return
	}
	q, err := w.Store.GetInteropQuoteByTransfer(r.Context(), w.Tenant, id)
	if err != nil {
		backendError(rw, err)
		return
	}
	if err = validateIncomingPrepare(q, request, w.FSPID); err != nil {
		backendError(rw, err)
		return
	}
	response, _ := json.Marshal(map[string]string{"homeTransactionId": id.String(), "transferState": "RESERVED", "completedTimestamp": time.Now().UTC().Format(time.RFC3339Nano)})
	prepareExpires, _ := time.Parse(time.RFC3339Nano, request.Protocol.Prepare.Expiration)
	original, err := w.Store.ReserveInteropIncoming(r.Context(), w.Tenant, q.ID, raw, response, prepareExpires)
	if err != nil {
		backendError(rw, err)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(original)
}
func validateIncomingPrepare(q *walletstore.InteropQuote, request IncomingPrepare, fsp string) error {
	var quote IncomingQuote
	var accepted BackendQuoteResponse
	if q.Direction != "IN" || json.Unmarshal(q.Request, &quote) != nil || json.Unmarshal(q.Response, &accepted) != nil {
		return ErrProtocol
	}
	p := request.Protocol.Prepare
	response := request.Protocol.Quote.Response
	if request.TransferID != q.TransferID.String() || p.TransferID != request.TransferID || request.Protocol.Source != quote.From.FSPID || p.PayerFSP != quote.From.FSPID || p.PayeeFSP != fsp || !moneyMatches(p.Amount, q.Amount, q.Currency) || !equalJSON(request.Protocol.Quote.Request, quote.Protocol.Request) || p.ILPPacket != response.ILPPacket || p.Condition != response.Condition || !moneyMatches(response.TransferAmount, q.Amount, q.Currency) || !zeroFee(response.PayeeFSPFee, q.Currency) || !zeroFee(response.PayeeFSPCommission, q.Currency) || response.Expiration != accepted.Expiration {
		return ErrProtocol
	}
	expiry, err := time.Parse(time.RFC3339Nano, p.Expiration)
	if err != nil || expiry.After(q.ExpiresAt.Time) {
		return ErrProtocol
	}
	if !ValidFulfilment(request.Protocol.Quote.Fulfilment, p.Condition) {
		return ErrProtocol
	}
	// Store checks expiry for NEW reservations and replays existing responses.
	return validatePacket(p.ILPPacket, q.TransferID, q.Amount, q.Currency, quote.To)
}
func equalJSON(a, b []byte) bool {
	var left, right any
	d1, d2 := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	d1.UseNumber()
	d2.UseNumber()
	return d1.Decode(&left) == nil && d2.Decode(&right) == nil && reflect.DeepEqual(left, right)
}
func (w *Worker) notification(rw http.ResponseWriter, r *http.Request) {
	var state SDKState
	raw, err := readJSON(r, &state)
	if err != nil {
		backendError(rw, err)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		backendError(rw, ErrProtocol)
		return
	}
	if err = w.acceptOutcome(r.Context(), id, state, raw, "sdk-loopback"); err != nil {
		backendError(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]bool{"accepted": true})
}
func (w *Worker) incomingStatus(rw http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		backendError(rw, ErrProtocol)
		return
	}
	q, err := w.Store.GetInteropQuoteByTransfer(r.Context(), w.Tenant, id)
	if err != nil {
		backendError(rw, err)
		return
	}
	t, err := w.Store.GetInteropTransfer(r.Context(), w.Tenant, q.OwnerID, id, "")
	if err != nil {
		backendError(rw, err)
		return
	}
	var response map[string]any
	if json.Unmarshal(t.OriginalResponse, &response) != nil {
		backendError(rw, ErrProtocol)
		return
	}
	state := "RESERVED"
	if t.HubState != "UNKNOWN" {
		state = t.HubState
	}
	var quote IncomingQuote
	var original IncomingPrepare
	if q.Direction != "IN" || json.Unmarshal(q.Request, &quote) != nil || json.Unmarshal(t.OriginalPrepare, &original) != nil || !ValidFulfilment(original.Protocol.Quote.Fulfilment, original.Protocol.Prepare.Condition) {
		backendError(rw, ErrProtocol)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"homeTransactionId": id.String(), "transferState": state, "timestamp": response["completedTimestamp"], "from": quote.From, "to": quote.To, "amountType": quote.AmountType, "expiration": original.Protocol.Prepare.Expiration, "currency": q.Currency, "amount": Decimal(q.Amount), "transactionType": quote.TransactionType, "fulfilment": original.Protocol.Quote.Fulfilment})
}
