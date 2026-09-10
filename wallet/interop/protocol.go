// Package interop implements the pinned noebs SDG/MSISDN/zero-fee profile.
// SDK convenience states never constitute a monetary authorization.
package interop

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

var ErrProtocol = errors.New("invalid_mojaloop_protocol")
var msisdn = regexp.MustCompile(`^249[0-9]{9}$`)
var minorPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`)

func ParseMinor(value string) (int64, error) {
	if !minorPattern.MatchString(value) {
		return 0, walletstore.ErrInvalidAmount
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount <= 0 {
		return 0, walletstore.ErrInvalidAmount
	}
	return amount, nil
}
func Decimal(amount int64) string { return fmt.Sprintf("%d.%02d", amount/100, amount%100) }

// ProtocolDecimal emits the SDK canonical Amount representation, before any
// protocol intent is persisted. Received payloads and stored replays keep their
// original bytes. No rounding or floating point conversion is performed.
func ProtocolDecimal(amount int64) string {
	return strings.TrimSuffix(strings.TrimRight(Decimal(amount), "0"), ".")
}

const protocolTimeFormat = "2006-01-02T15:04:05.000Z"

func ParseDecimal(value string) (int64, error) {
	if len(value) > 22 || !decimalPattern.MatchString(value) {
		return 0, ErrProtocol
	}
	parts := strings.Split(value, ".")
	fraction := "00"
	if len(parts) == 2 {
		fraction = (parts[1] + "0")[:2]
	}
	n, err := strconv.ParseInt(parts[0]+fraction, 10, 64)
	if err != nil {
		return 0, ErrProtocol
	}
	return n, nil
}

type Party struct {
	IDType      string `json:"idType"`
	IDValue     string `json:"idValue"`
	FSPID       string `json:"fspId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}
type QuoteIntent struct {
	From            Party  `json:"from"`
	To              Party  `json:"to"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	AmountType      string `json:"amountType"`
	TransactionType string `json:"transactionType"`
}
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}
type QuoteResponse struct {
	TransferAmount     Money  `json:"transferAmount"`
	PayeeReceiveAmount *Money `json:"payeeReceiveAmount,omitempty"`
	PayeeFSPFee        *Money `json:"payeeFspFee,omitempty"`
	PayeeFSPCommission *Money `json:"payeeFspCommission,omitempty"`
	Expiration         string `json:"expiration"`
	ILPPacket          string `json:"ilpPacket"`
	Condition          string `json:"condition"`
}
type Prepare struct {
	TransferID string `json:"transferId"`
	PayerFSP   string `json:"payerFsp"`
	PayeeFSP   string `json:"payeeFsp"`
	Amount     Money  `json:"amount"`
	ILPPacket  string `json:"ilpPacket"`
	Condition  string `json:"condition"`
	Expiration string `json:"expiration"`
}
type Fulfil struct {
	TransferState      string `json:"transferState"`
	Fulfilment         string `json:"fulfilment,omitempty"`
	CompletedTimestamp string `json:"completedTimestamp,omitempty"`
}
type Envelope[T any] struct {
	Headers ProtocolHeaders `json:"headers"`
	Body    T               `json:"body"`
}
type NativeParty struct {
	Name string `json:"name"`
	ID   struct {
		Type  string `json:"partyIdType"`
		Value string `json:"partyIdentifier"`
		FSP   string `json:"fspId"`
	} `json:"partyIdInfo"`
}

func (p NativeParty) matches(party Party) bool {
	return p.ID.Type == party.IDType && p.ID.Value == party.IDValue && p.ID.FSP == party.FSPID
}

type PartiesResponse struct {
	Party NativeParty `json:"party"`
}
type SDKState struct {
	HomeTransactionID       string                    `json:"homeTransactionId"`
	TransferID              string                    `json:"transferId"`
	QuoteID                 string                    `json:"quoteId"`
	CurrentState            string                    `json:"currentState"`
	To                      Party                     `json:"to"`
	From                    Party                     `json:"from"`
	GetPartiesResponse      Envelope[PartiesResponse] `json:"getPartiesResponse"`
	QuoteRequest            Envelope[json.RawMessage] `json:"quoteRequest"`
	QuoteResponse           Envelope[QuoteResponse]   `json:"quoteResponse"`
	Prepare                 Envelope[Prepare]         `json:"prepare"`
	Fulfil                  Envelope[Fulfil]          `json:"fulfil"`
	FinalNotification       *Fulfil                   `json:"finalNotification,omitempty"`
	FinalNotificationSource string                    `json:"finalNotificationSource,omitempty"`
}

func ValidFulfilment(fulfilment, condition string) bool {
	f, err := base64.RawURLEncoding.DecodeString(fulfilment)
	if err != nil || len(f) != 32 {
		return false
	}
	c, err := base64.RawURLEncoding.DecodeString(condition)
	if err != nil || len(c) != 32 {
		return false
	}
	digest := sha256.Sum256(f)
	return subtle.ConstantTimeCompare(c, digest[:]) == 1
}

func moneyMatches(m Money, amount int64, currency string) bool {
	n, err := ParseDecimal(m.Amount)
	return err == nil && n == amount && m.Currency == currency
}
func zeroFee(m *Money, currency string) bool { return m == nil || moneyMatches(*m, 0, currency) }

func ValidateQuote(q *walletstore.InteropQuote, s SDKState, fsp string, now time.Time) error {
	var intent QuoteIntent
	if err := json.Unmarshal(q.Request, &intent); err != nil {
		return ErrProtocol
	}
	if s.HomeTransactionID != q.TransferID.String() || s.TransferID != q.TransferID.String() || s.CurrentState != "WAITING_FOR_QUOTE_ACCEPTANCE" || s.To.IDType != "MSISDN" || s.To.IDValue != intent.To.IDValue || s.To.FSPID == "" || s.To.FSPID == fsp || s.From != intent.From || intent.From.FSPID != fsp {
		return ErrProtocol
	}
	party := s.GetPartiesResponse
	var native NativeQuote
	if !party.Body.Party.matches(s.To) || strings.TrimSpace(party.Body.Party.Name) == "" || party.Headers["fspiop-source"] != s.To.FSPID || party.Headers["fspiop-destination"] != fsp || json.Unmarshal(s.QuoteRequest.Body, &native) != nil || native.QuoteID != s.QuoteID || native.TransactionID != q.TransferID.String() || !native.Payer.matches(intent.From) || !native.Payee.matches(s.To) || !moneyMatches(native.Amount, q.Amount, q.Currency) || native.AmountType != "SEND" || native.TransactionType.Scenario != "TRANSFER" || native.TransactionType.Initiator != "PAYER" || native.TransactionType.InitiatorType != "CONSUMER" || !zeroFee(native.Fees, q.Currency) || s.QuoteRequest.Headers["fspiop-source"] != fsp || s.QuoteRequest.Headers["fspiop-destination"] != s.To.FSPID {
		return ErrProtocol
	}
	if _, err := uuid.Parse(s.QuoteID); err != nil {
		return ErrProtocol
	}
	response := s.QuoteResponse.Body
	if !moneyMatches(response.TransferAmount, q.Amount, q.Currency) || !zeroFee(response.PayeeFSPFee, q.Currency) || !zeroFee(response.PayeeFSPCommission, q.Currency) {
		return ErrProtocol
	}
	if response.PayeeReceiveAmount != nil && !moneyMatches(*response.PayeeReceiveAmount, q.Amount, q.Currency) {
		return ErrProtocol
	}
	if s.QuoteResponse.Headers["fspiop-source"] != s.To.FSPID || s.QuoteResponse.Headers["fspiop-destination"] != fsp {
		return ErrProtocol
	}
	expiry, err := time.Parse(time.RFC3339Nano, response.Expiration)
	if err != nil || !expiry.After(now.Add(5*time.Second)) {
		return walletstore.ErrInteropQuoteExpired
	}
	cond, err := base64.RawURLEncoding.DecodeString(response.Condition)
	if err != nil || len(cond) != 32 {
		return ErrProtocol
	}
	return validatePacket(response.ILPPacket, q.TransferID, q.Amount, q.Currency, s.To)
}

func PrepareFromQuote(q *walletstore.InteropQuote, fsp string) (Prepare, error) {
	var state SDKState
	if err := json.Unmarshal(q.SDKState, &state); err != nil {
		return Prepare{}, err
	}
	r := state.QuoteResponse.Body
	if !moneyMatches(r.TransferAmount, q.Amount, q.Currency) {
		return Prepare{}, ErrProtocol
	}
	return Prepare{TransferID: q.TransferID.String(), PayerFSP: fsp, PayeeFSP: state.To.FSPID, Amount: r.TransferAmount, ILPPacket: r.ILPPacket, Condition: r.Condition, Expiration: r.Expiration}, nil
}

// ValidateOutcome is called only for a response read from the literal loopback
// SDK endpoint or its private backend callback. It correlates stored terms and
// checks the fulfilment independently, including SDK hub-GET responses.
func ValidateOutcome(q *walletstore.InteropQuote, t *walletstore.InteropTransfer, state SDKState, authority, fsp string) (string, error) {
	if state.TransferID != "" && state.TransferID != q.TransferID.String() {
		return "", ErrProtocol
	}
	if (q.Direction == "OUT" && !t.SubmittedAt.Valid) || (q.Direction == "IN" && len(t.OriginalResponse) == 0) {
		return "", ErrProtocol
	}
	var expected Prepare
	if q.Direction == "OUT" {
		var err error
		expected, err = PrepareFromQuote(q, fsp)
		if err != nil {
			return "", err
		}
	} else {
		var original IncomingPrepare
		if err := json.Unmarshal(t.OriginalPrepare, &original); err != nil {
			return "", ErrProtocol
		}
		expected = original.Protocol.Prepare
	}
	if state.Prepare.Body.TransferID != "" && !samePrepareTerms(expected, state.Prepare.Body) {
		return "", ErrProtocol
	}
	var terminal Fulfil
	if authority == "sdk-loopback" && q.Direction == "IN" {
		if state.FinalNotification == nil || (state.FinalNotificationSource != "switch" && state.FinalNotificationSource != "hub") {
			return "", ErrProtocol
		}
		terminal = *state.FinalNotification
		// Native hub PATCH may omit fulfilment; SDK-private provenance and the
		// immutable reservation bind the notification to the validated prepare.
	} else {
		terminal = state.Fulfil.Body
		source := state.Fulfil.Headers["fspiop-source"]
		if source != "switch" && source != "hub" && source != expected.PayeeFSP {
			return "", ErrProtocol
		}
		if state.Fulfil.Headers["fspiop-destination"] != "" && state.Fulfil.Headers["fspiop-destination"] != fsp {
			return "", ErrProtocol
		}
		if terminal.TransferState == "COMMITTED" && !ValidFulfilment(terminal.Fulfilment, expected.Condition) {
			return "", ErrProtocol
		}
	}
	if terminal.Fulfilment != "" && !ValidFulfilment(terminal.Fulfilment, expected.Condition) {
		return "", ErrProtocol
	}
	if terminal.TransferState != "COMMITTED" && terminal.TransferState != "ABORTED" {
		return "", nil
	}
	return terminal.TransferState, nil
}
func samePrepareTerms(a, b Prepare) bool {
	return a.TransferID == b.TransferID && a.PayerFSP == b.PayerFSP && a.PayeeFSP == b.PayeeFSP && a.Amount == b.Amount && a.ILPPacket == b.ILPPacket && a.Condition == b.Condition && a.Expiration == b.Expiration
}

// Decode only the supported FSPIOP ILP-v1 OER profile, with bounded lengths and
// exact transaction, destination and integer-amount correlation.
func validatePacket(encoded string, id uuid.UUID, amount int64, currency string, payee Party) error {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(data) < 2 || len(data) > 32768 || data[0] != 1 {
		return ErrProtocol
	}
	body, rest, err := octets(data[1:])
	if err != nil || len(rest) != 0 || len(body) < 11 || binary.BigEndian.Uint64(body[:8]) != uint64(amount) {
		return ErrProtocol
	}
	destination, rest, err := octets(body[8:])
	if err != nil || string(destination) != "g."+payee.FSPID+".msisdn."+payee.IDValue {
		return ErrProtocol
	}
	payload, rest, err := octets(rest)
	if err != nil || len(rest) != 1 || rest[0] != 0 {
		return ErrProtocol
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(payload))
	if err != nil {
		return ErrProtocol
	}
	var txn struct {
		TransactionID string `json:"transactionId"`
		Amount        Money  `json:"amount"`
	}
	if json.Unmarshal(decoded, &txn) != nil || txn.TransactionID != id.String() || !moneyMatches(txn.Amount, amount, currency) {
		return ErrProtocol
	}
	return nil
}
func octets(data []byte) ([]byte, []byte, error) {
	if len(data) < 1 {
		return nil, nil, ErrProtocol
	}
	n := int(data[0])
	offset := 1
	if n >= 128 {
		size := n & 127
		if size == 0 || size > 4 || len(data) < 1+size {
			return nil, nil, ErrProtocol
		}
		n = 0
		for _, v := range data[1 : 1+size] {
			n = (n << 8) | int(v)
		}
		offset += size
	}
	if n > len(data)-offset {
		return nil, nil, ErrProtocol
	}
	return data[offset : offset+n], data[offset+n:], nil
}
