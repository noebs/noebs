package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type Worker struct {
	Store   *walletstore.Store
	Tenant  string
	FSPID   string
	client  *http.Client
	running atomic.Bool
}

func NewWorker(ctx context.Context, store *walletstore.Store, tenant, fsp string) (*Worker, error) {
	if store == nil || fsp == "" {
		return nil, walletstore.ErrInteropInvalid
	}
	b, err := store.GetInteropBinding(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if b.FSPID != fsp {
		return nil, walletstore.ErrInteropInvalid
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	return &Worker{Store: store, Tenant: tenant, FSPID: fsp, client: &http.Client{Transport: transport, Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Start must succeed before the worker is considered ready. No SDK backend
// route is mounted on the application's external/background health listeners.
func (w *Worker) Start(ctx context.Context) (*http.Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:4002")
	if err != nil {
		return nil, err
	}
	server := &http.Server{Addr: "127.0.0.1:4002", Handler: w.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second}
	runCtx, cancel := context.WithCancel(ctx)
	w.running.Store(true)
	go func() {
		defer cancel()
		defer w.running.Store(false)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("interop backend stopped", "error", err)
		}
	}()
	go func() {
		<-runCtx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 20*time.Second)
		defer c()
		_ = server.Shutdown(shutdown)
	}()
	go w.run(runCtx)
	return server, nil
}

func (w *Worker) Ready(ctx context.Context) bool {
	if !w.running.Load() {
		return false
	}
	check, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return w.Store.DB.PingContext(check) == nil
}
func (w *Worker) run(ctx context.Context) {
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		if err := w.Process(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("interop work deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (w *Worker) Process(ctx context.Context) error {
	// Receipt recovery precedes new dispatch, including obligations which were
	// already authoritative COMMITTED when the previous process stopped.
	ids, err := w.Store.PendingInteropEvents(ctx, w.Tenant)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = w.Store.ApplyInteropEvent(ctx, w.Tenant, id); err != nil {
			_ = w.Store.DeferInteropEvent(ctx, w.Tenant, id)
			slog.Warn("interop posting deferred", "event_id", id)
		}
	}
	q, err := w.Store.ClaimInteropQuote(ctx, w.Tenant, uuid.New())
	if err == nil {
		w.quote(ctx, q)
	} else if !errors.Is(err, walletstore.ErrInteropNotFound) {
		return err
	}
	t, err := w.Store.ClaimInteropTransfer(ctx, w.Tenant, uuid.New())
	if errors.Is(err, walletstore.ErrInteropNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return w.transfer(ctx, t)
}
func (w *Worker) sdk(ctx context.Context, method, path string, body []byte) (SDKState, []byte, error) {
	var state SDKState
	request, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:4001"+path, bytes.NewReader(body))
	if err != nil {
		return state, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := w.client.Do(request)
	if err != nil {
		return state, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return state, nil, ErrProtocol
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return state, raw, fmt.Errorf("sdk_http_%d", response.StatusCode)
	}
	if json.Unmarshal(raw, &state) != nil {
		return state, raw, ErrProtocol
	}
	return state, raw, nil
}
func (w *Worker) quote(ctx context.Context, q *walletstore.InteropQuote) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(q.Request, &fields) != nil {
		return
	}
	fields["transferId"], _ = json.Marshal(q.TransferID.String())
	raw, _ := json.Marshal(fields)
	state, response, err := w.sdk(ctx, http.MethodPost, "/transfers", raw)
	if err == nil {
		err = ValidateQuote(q, state, w.FSPID, time.Now())
	}
	if err != nil {
		_ = w.Store.CompleteInteropQuote(ctx, w.Tenant, q.ID, q.LeaseToken.UUID, nil, nil, time.Time{}, "quote_unavailable")
		return
	}
	expires, _ := time.Parse(time.RFC3339Nano, state.QuoteResponse.Body.Expiration)
	terms, _ := json.Marshal(state.QuoteResponse.Body)
	if err = w.Store.CompleteInteropQuote(ctx, w.Tenant, q.ID, q.LeaseToken.UUID, terms, response, expires, ""); err != nil {
		slog.Warn("interop quote completion rejected", "quote_id", q.ID, "error", err)
	}
}
func (w *Worker) transfer(ctx context.Context, t *walletstore.InteropTransfer) error {
	q, err := w.Store.GetInteropQuote(ctx, w.Tenant, t.QuoteID)
	if err != nil {
		return err
	}
	token := t.LeaseToken.UUID
	if t.SubmittedAt.Valid || q.Direction == "IN" {
		state, raw, queryErr := w.sdk(ctx, http.MethodGet, "/transfers/"+t.ID.String(), nil)
		terminal := false
		if queryErr == nil {
			outcome, validationErr := ValidateOutcome(q, t, state, "sdk-hub-query", w.FSPID)
			terminal = validationErr == nil && outcome != ""
			queryErr = w.acceptOutcome(ctx, t.ID, state, raw, "sdk-hub-query")
		}
		// A hub query may change the SDK convenience cache. Replays use only
		// the immutable SQL obligation and the native SDK primitive.
		if !terminal && t.HubState == "UNKNOWN" && q.ExpiresAt.Time.After(time.Now().Add(5*time.Second)) {
			if q.Direction == "OUT" {
				_ = w.submitPrepare(ctx, q, t.OriginalPrepare)
			} else {
				_ = w.replayReservation(ctx, q, t)
			}
		}
		code := "awaiting_hub_commit"
		if queryErr != nil {
			code = "hub_outcome_unresolved"
		}
		return w.Store.DeferInteropTransfer(ctx, w.Tenant, t.ID, token, code)
	}
	if t.Status == "REQUESTED" {
		t, err = w.Store.ArmInteropTransfer(ctx, w.Tenant, t.ID, token)
		if err != nil {
			if admissionFailure(err) {
				return w.Store.RejectInteropBeforeSubmit(ctx, w.Tenant, q.TransferID, token, "transfer_not_eligible")
			}
			_ = w.Store.DeferInteropTransfer(ctx, w.Tenant, q.TransferID, token, "admission_unavailable")
			return err
		}
	}
	prepare, err := PrepareFromQuote(q, w.FSPID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(prepare)
	if !q.ExpiresAt.Time.After(time.Now().Add(5 * time.Second)) {
		return w.Store.RejectInteropBeforeSubmit(ctx, w.Tenant, t.ID, token, "quote_expired")
	}
	if err = w.Store.MarkInteropSubmitted(ctx, w.Tenant, t.ID, token, payload); err != nil {
		_ = w.Store.DeferInteropTransfer(ctx, w.Tenant, t.ID, token, "dispatch_deferred")
		return err
	}
	_ = w.submitPrepare(ctx, q, payload)
	// Even an HTTP failure might follow a committed hub submission. Keep the
	// obligation and let the next iteration issue a native hub GET.
	return w.Store.DeferInteropTransfer(ctx, w.Tenant, t.ID, token, "awaiting_hub_commit")
}

func (w *Worker) submitPrepare(ctx context.Context, q *walletstore.InteropQuote, rawPrepare []byte) error {
	var prepare Prepare
	expected, err := PrepareFromQuote(q, w.FSPID)
	if err != nil || json.Unmarshal(rawPrepare, &prepare) != nil || !samePrepareTerms(expected, prepare) {
		return ErrProtocol
	}
	expires, err := time.Parse(time.RFC3339Nano, prepare.Expiration)
	if err != nil || !expires.After(time.Now().Add(5*time.Second)) {
		return walletstore.ErrInteropQuoteExpired
	}
	body, err := json.Marshal(map[string]any{"fspId": prepare.PayeeFSP, "transfersPostRequest": json.RawMessage(rawPrepare)})
	if err != nil {
		return err
	}
	_, raw, err := w.sdk(ctx, http.MethodPost, "/simpleTransfers", body)
	if err != nil {
		return err
	}
	var response struct {
		Transfer Envelope[Fulfil] `json:"transfer"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return ErrProtocol
	}
	state := SDKState{TransferID: q.TransferID.String(), Prepare: Envelope[Prepare]{Body: prepare}, Fulfil: response.Transfer}
	// Retain the actual native transport response in the inbox, while validating
	// its typed envelope against the durable preparation.
	return w.acceptOutcome(ctx, q.TransferID, state, raw, "sdk-loopback")
}

func (w *Worker) replayReservation(ctx context.Context, q *walletstore.InteropQuote, t *walletstore.InteropTransfer) error {
	var original IncomingPrepare
	if json.Unmarshal(t.OriginalPrepare, &original) != nil {
		return ErrProtocol
	}
	expires, err := time.Parse(time.RFC3339Nano, original.Protocol.Prepare.Expiration)
	if err != nil || !expires.After(time.Now().Add(5*time.Second)) {
		return walletstore.ErrInteropQuoteExpired
	}
	// The patched native inbound GET obtains the original fulfilment and reserved
	// timestamp from SQL, then sends the same PUT through MojaloopRequests. This
	// also works after Redis loss and does not grant new spending authority.
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:4000/transfers/"+q.TransferID.String(), nil)
	if err != nil {
		return err
	}
	r.Header.Set("FSPIOP-Source", original.Protocol.Prepare.PayerFSP)
	r.Header.Set("FSPIOP-Destination", w.FSPID)
	r.Header.Set("Accept", "application/vnd.interoperability.transfers+json;version=1.1")
	r.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	response, err := w.client.Do(r)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return fmt.Errorf("sdk_replay_http_%d", response.StatusCode)
	}
	return nil
}
func admissionFailure(err error) bool {
	var limit walletstore.TransactionLimitExceededError
	return errors.Is(err, walletstore.ErrInteropQuoteExpired) || errors.Is(err, walletstore.ErrInteropDisabled) || errors.Is(err, walletstore.ErrInteropState) || errors.Is(err, walletstore.ErrInsufficientFunds) || errors.Is(err, walletstore.ErrWalletInactive) || errors.Is(err, walletstore.ErrTransactionLimitNotFound) || errors.As(err, &limit)
}
func (w *Worker) acceptOutcome(ctx context.Context, id uuid.UUID, state SDKState, raw []byte, authority string) error {
	q, err := w.Store.GetInteropQuoteByTransfer(ctx, w.Tenant, id)
	if err != nil {
		return err
	}
	t, err := w.Store.GetInteropTransfer(ctx, w.Tenant, q.OwnerID, id, "")
	if err != nil {
		return err
	}
	outcome, err := ValidateOutcome(q, t, state, authority, w.FSPID)
	if err != nil {
		return err
	}
	if outcome == "" {
		return nil
	}
	_, err = w.Store.RecordInteropEvent(ctx, walletstore.InteropEvent{TenantID: w.Tenant, TransferID: id, Kind: outcome, Authority: authority, Payload: raw})
	return err
}
