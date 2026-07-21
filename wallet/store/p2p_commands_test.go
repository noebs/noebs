package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestP2PCommandStoreValidatesBeforeDatabaseAccess(t *testing.T) {
	store := &Store{}
	fromWalletID := uuid.New()
	toWalletID := uuid.New()
	valid := P2PCommandReservation{
		TenantID:       "tenant",
		IdempotencyKey: "p2p-1",
		WorkflowID:     "wallet-p2p-workflow",
		FromWalletID:   fromWalletID,
		ToWalletID:     toWalletID,
		FromOwnerType:  OwnerTypeUser,
		FromOwnerID:    "source",
		ToOwnerType:    OwnerTypeUser,
		ToOwnerID:      "destination",
		Command:        RawJSON(`{"Amount":100}`),
	}
	for _, test := range []struct {
		name   string
		mutate func(*P2PCommandReservation)
		want   error
	}{
		{name: "missing tenant", mutate: func(command *P2PCommandReservation) { command.TenantID = "" }, want: ErrMissingTenantID},
		{name: "invalid tenant", mutate: func(command *P2PCommandReservation) { command.TenantID = " default " }, want: ErrInvalidTenantID},
		{name: "missing idempotency", mutate: func(command *P2PCommandReservation) { command.IdempotencyKey = "" }, want: ErrMissingIdempotencyKey},
		{name: "blank idempotency", mutate: func(command *P2PCommandReservation) { command.IdempotencyKey = " " }, want: ErrInvalidIdempotencyKey},
		{name: "long idempotency", mutate: func(command *P2PCommandReservation) { command.IdempotencyKey = strings.Repeat("x", 257) }, want: ErrInvalidIdempotencyKey},
		{name: "missing workflow", mutate: func(command *P2PCommandReservation) { command.WorkflowID = "" }, want: ErrMissingWorkflowID},
		{name: "blank workflow", mutate: func(command *P2PCommandReservation) { command.WorkflowID = " workflow " }, want: ErrInvalidWorkflowID},
		{name: "long workflow", mutate: func(command *P2PCommandReservation) { command.WorkflowID = strings.Repeat("x", 256) }, want: ErrInvalidWorkflowID},
		{name: "missing source wallet", mutate: func(command *P2PCommandReservation) { command.FromWalletID = uuid.Nil }, want: ErrMissingWalletID},
		{name: "same wallet", mutate: func(command *P2PCommandReservation) { command.ToWalletID = command.FromWalletID }, want: ErrInvalidWalletPair},
		{name: "missing owner type", mutate: func(command *P2PCommandReservation) { command.FromOwnerType = "" }, want: ErrMissingOwnerType},
		{name: "invalid owner type", mutate: func(command *P2PCommandReservation) { command.ToOwnerType = "invalid" }, want: ErrInvalidOwnerType},
		{name: "missing owner id", mutate: func(command *P2PCommandReservation) { command.FromOwnerID = "" }, want: ErrMissingOwnerID},
		{name: "missing command", mutate: func(command *P2PCommandReservation) { command.Command = nil }, want: ErrMissingP2PCommand},
		{name: "invalid command", mutate: func(command *P2PCommandReservation) { command.Command = RawJSON(`{`) }, want: ErrInvalidP2PCommand},
		{name: "non-object command", mutate: func(command *P2PCommandReservation) { command.Command = RawJSON(`[]`) }, want: ErrInvalidP2PCommand},
	} {
		t.Run("reserve "+test.name, func(t *testing.T) {
			command := valid
			test.mutate(&command)
			_, err := store.ReserveP2PCommand(context.Background(), command)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if errors.Is(err, ErrMissingStore) {
				t.Fatal("validation reached database")
			}
		})
	}

	for _, test := range []struct {
		name           string
		tenantID       string
		idempotencyKey string
		want           error
	}{
		{name: "tenant", tenantID: "", idempotencyKey: "p2p-1", want: ErrMissingTenantID},
		{name: "missing idempotency", tenantID: "tenant", idempotencyKey: "", want: ErrMissingIdempotencyKey},
		{name: "invalid idempotency", tenantID: "tenant", idempotencyKey: " key ", want: ErrInvalidIdempotencyKey},
	} {
		t.Run("get "+test.name, func(t *testing.T) {
			_, err := store.GetP2PCommand(context.Background(), test.tenantID, test.idempotencyKey)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	for _, test := range []struct {
		name                  string
		tenant, key, workflow string
		run                   string
		want                  error
	}{
		{name: "tenant", key: "p2p-1", workflow: "workflow", run: "run", want: ErrMissingTenantID},
		{name: "idempotency", tenant: "tenant", workflow: "workflow", run: "run", want: ErrMissingIdempotencyKey},
		{name: "invalid idempotency", tenant: "tenant", key: " key ", workflow: "workflow", run: "run", want: ErrInvalidIdempotencyKey},
		{name: "workflow", tenant: "tenant", key: "p2p-1", run: "run", want: ErrMissingWorkflowID},
		{name: "invalid workflow", tenant: "tenant", key: "p2p-1", workflow: " workflow ", run: "run", want: ErrInvalidWorkflowID},
		{name: "run", tenant: "tenant", key: "p2p-1", workflow: "workflow", want: ErrMissingWorkflowRunID},
		{name: "invalid run", tenant: "tenant", key: "p2p-1", workflow: "workflow", run: " run ", want: ErrInvalidWorkflowRunID},
	} {
		t.Run("record "+test.name, func(t *testing.T) {
			_, err := store.RecordP2PCommandRun(context.Background(), test.tenant, test.key, test.workflow, test.run)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateP2PCommandReplayRequiresExactCommand(t *testing.T) {
	fromWalletID := uuid.New()
	toWalletID := uuid.New()
	requested := P2PCommandReservation{
		TenantID:       "tenant",
		IdempotencyKey: "p2p-1",
		WorkflowID:     "workflow-1",
		FromWalletID:   fromWalletID,
		ToWalletID:     toWalletID,
		FromOwnerType:  OwnerTypeUser,
		FromOwnerID:    "source",
		ToOwnerType:    OwnerTypeUser,
		ToOwnerID:      "destination",
		Command:        RawJSON(`{"Amount":100,"ReferenceID":"ref-1","ToWalletID":"wallet-2"}`),
	}
	existing := P2PCommand{
		TenantID:       requested.TenantID,
		IdempotencyKey: requested.IdempotencyKey,
		WorkflowID:     requested.WorkflowID,
		FromWalletID:   requested.FromWalletID,
		ToWalletID:     requested.ToWalletID,
		FromOwnerType:  requested.FromOwnerType,
		FromOwnerID:    requested.FromOwnerID,
		ToOwnerType:    requested.ToOwnerType,
		ToOwnerID:      requested.ToOwnerID,
		Command:        requested.Command,
	}
	if err := ValidateP2PCommandReplay(&existing, requested); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	reformatted := requested
	reformatted.Command = RawJSON(`{"ToWalletID":"wallet-2", "ReferenceID":"ref-1", "Amount":100}`)
	if err := ValidateP2PCommandReplay(&existing, reformatted); err != nil {
		t.Fatalf("semantic JSON replay: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*P2PCommandReservation)
	}{
		{name: "tenant", mutate: func(command *P2PCommandReservation) { command.TenantID = "other" }},
		{name: "idempotency", mutate: func(command *P2PCommandReservation) { command.IdempotencyKey = "other" }},
		{name: "workflow", mutate: func(command *P2PCommandReservation) { command.WorkflowID = "other" }},
		{name: "source wallet", mutate: func(command *P2PCommandReservation) { command.FromWalletID = uuid.New() }},
		{name: "destination wallet", mutate: func(command *P2PCommandReservation) { command.ToWalletID = uuid.New() }},
		{name: "source owner", mutate: func(command *P2PCommandReservation) { command.FromOwnerID = "other" }},
		{name: "destination owner", mutate: func(command *P2PCommandReservation) { command.ToOwnerType = OwnerTypeMerchant }},
		{name: "command", mutate: func(command *P2PCommandReservation) {
			command.Command = RawJSON(`{"Amount":101,"ReferenceID":"ref-1","ToWalletID":"wallet-2"}`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := requested
			test.mutate(&changed)
			if err := ValidateP2PCommandReplay(&existing, changed); !errors.Is(err, ErrDuplicateP2PCommand) {
				t.Fatalf("error = %v, want %v", err, ErrDuplicateP2PCommand)
			}
		})
	}
	if err := ValidateP2PCommandReplay(nil, requested); !errors.Is(err, ErrDuplicateP2PCommand) {
		t.Fatalf("nil existing error = %v, want %v", err, ErrDuplicateP2PCommand)
	}
	largeExisting := existing
	largeExisting.Command = RawJSON(`{"Amount":9007199254740992}`)
	largeRequested := requested
	largeRequested.Command = RawJSON(`{"Amount":9007199254740993}`)
	if err := ValidateP2PCommandReplay(&largeExisting, largeRequested); !errors.Is(err, ErrDuplicateP2PCommand) {
		t.Fatalf("large integer mismatch error = %v, want %v", err, ErrDuplicateP2PCommand)
	}
}

func TestDecodeP2PCommandRequiresExactWorkflowBinding(t *testing.T) {
	fromWalletID := uuid.MustParse("5d6eea5d-3059-49f9-8bc3-22bc7407b8b7")
	toWalletID := uuid.MustParse("548c3fca-f384-4659-a8b7-7a3b6e779b94")
	command := &P2PCommand{
		TenantID: "tenant", IdempotencyKey: "p2p-1", WorkflowID: "workflow-1",
		FromWalletID: fromWalletID, ToWalletID: toWalletID,
		FromOwnerType: OwnerTypeUser, FromOwnerID: "1", ToOwnerType: OwnerTypeUser, ToOwnerID: "2",
		Command: RawJSON(`{"currency":"AED","from_wallet_id":"5d6eea5d-3059-49f9-8bc3-22bc7407b8b7","to_wallet_id":"548c3fca-f384-4659-a8b7-7a3b6e779b94","amount":100,"reference_id":"ref-1","from_owner_type":"user","from_owner_id":"1","to_owner_type":"user","to_owner_id":"2"}`),
	}
	payload, err := DecodeP2PCommand(command, "tenant", "p2p-1", "workflow-1")
	if err != nil {
		t.Fatalf("decode exact command: %v", err)
	}
	if payload.Amount != 100 || payload.Currency != "AED" || payload.ReferenceID != "ref-1" {
		t.Fatalf("decoded payload = %+v", payload)
	}
	for _, test := range []struct {
		name                  string
		tenant, key, workflow string
	}{
		{name: "tenant", tenant: "other", key: "p2p-1", workflow: "workflow-1"},
		{name: "idempotency", tenant: "tenant", key: "other", workflow: "workflow-1"},
		{name: "workflow", tenant: "tenant", key: "p2p-1", workflow: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeP2PCommand(command, test.tenant, test.key, test.workflow); !errors.Is(err, ErrInvalidP2PCommand) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidP2PCommand)
			}
		})
	}
}

func TestP2PCommandReservationAndRunRecordingAreConcurrent(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)
	fromWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "p2p-source", UserID: 81,
		Currency: "AED", CurrencyUnitID: testCurrencyUnitID(t, ctx, store, "AED"), KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure source wallet: %v", err)
	}
	toWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "p2p-destination", UserID: 82,
		Currency: "AED", CurrencyUnitID: fromWallet.CurrencyUnitID, KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure destination wallet: %v", err)
	}
	command := P2PCommandReservation{
		TenantID:       tenantID,
		IdempotencyKey: "p2p-concurrent",
		WorkflowID:     "wallet-p2p-concurrent",
		FromWalletID:   fromWallet.ID,
		ToWalletID:     toWallet.ID,
		FromOwnerType:  fromWallet.OwnerType,
		FromOwnerID:    fromWallet.OwnerID,
		ToOwnerType:    toWallet.OwnerType,
		ToOwnerID:      toWallet.OwnerID,
		Command:        RawJSON(`{"Amount":100,"ReferenceID":"ref-1","ToWalletID":"wallet-2"}`),
	}

	const callers = 32
	start := make(chan struct{})
	reservations := make(chan *P2PCommand, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stored, err := store.ReserveP2PCommand(ctx, command)
			if err != nil {
				errs <- err
				return
			}
			reservations <- stored
		}()
	}
	close(start)
	wg.Wait()
	close(reservations)
	close(errs)
	for err := range errs {
		t.Errorf("equal concurrent reservation: %v", err)
	}
	for stored := range reservations {
		if err := ValidateP2PCommandReplay(stored, command); err != nil {
			t.Errorf("reserved command: %v", err)
		}
		if stored.RunID.Valid {
			t.Errorf("new reservation run = %+v, want NULL", stored.RunID)
		}
	}

	start = make(chan struct{})
	runs := make(chan *P2PCommand, callers)
	errs = make(chan error, callers)
	wg = sync.WaitGroup{}
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stored, err := store.RecordP2PCommandRun(ctx, tenantID, command.IdempotencyKey, command.WorkflowID, "run-1")
			if err != nil {
				errs <- err
				return
			}
			runs <- stored
		}()
	}
	close(start)
	wg.Wait()
	close(runs)
	close(errs)
	for err := range errs {
		t.Errorf("equal concurrent run record: %v", err)
	}
	for stored := range runs {
		if !stored.RunID.Valid || stored.RunID.String != "run-1" {
			t.Errorf("recorded run = %+v, want run-1", stored.RunID)
		}
	}
	if _, err := store.RecordP2PCommandRun(ctx, tenantID, command.IdempotencyKey, command.WorkflowID, "run-2"); !errors.Is(err, ErrDuplicateP2PCommand) {
		t.Fatalf("run overwrite error = %v, want %v", err, ErrDuplicateP2PCommand)
	}
	replayed, err := store.ReserveP2PCommand(ctx, command)
	if err != nil {
		t.Fatalf("reserve completed command: %v", err)
	}
	if !replayed.RunID.Valid || replayed.RunID.String != "run-1" {
		t.Fatalf("reservation overwrote run = %+v", replayed.RunID)
	}

	conflicting := command
	conflicting.IdempotencyKey = "p2p-conflict"
	conflicting.WorkflowID = "wallet-p2p-conflict"
	variants := []RawJSON{
		RawJSON(`{"Amount":100,"ReferenceID":"ref-1","ToWalletID":"wallet-2"}`),
		RawJSON(`{"Amount":101,"ReferenceID":"ref-1","ToWalletID":"wallet-2"}`),
	}
	type conflictResult struct {
		variant int
		stored  *P2PCommand
		err     error
	}
	results := make(chan conflictResult, callers)
	start = make(chan struct{})
	wg = sync.WaitGroup{}
	for caller := range callers {
		variant := caller % len(variants)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request := conflicting
			request.Command = variants[variant]
			stored, err := store.ReserveP2PCommand(ctx, request)
			results <- conflictResult{variant: variant, stored: stored, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winner, err := store.GetP2PCommand(ctx, tenantID, conflicting.IdempotencyKey)
	if err != nil {
		t.Fatalf("get conflicting reservation winner: %v", err)
	}
	winnerVariant := -1
	for variant := range variants {
		if rawJSONMatches(winner.Command, variants[variant]) {
			winnerVariant = variant
		}
	}
	if winnerVariant < 0 {
		t.Fatalf("stored command %s matches no submitted variant", winner.Command)
	}
	for result := range results {
		if result.variant == winnerVariant {
			if result.err != nil || result.stored == nil {
				t.Errorf("winning variant result = %+v, %v", result.stored, result.err)
			}
		} else if !errors.Is(result.err, ErrDuplicateP2PCommand) {
			t.Errorf("losing variant error = %v, want %v", result.err, ErrDuplicateP2PCommand)
		}
	}
}
