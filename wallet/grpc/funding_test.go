package walletgrpc

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCreateReturnToSourceDestinationRequiresLinkedFundingSource(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)
	_, err := server.CreateWithdrawalDestination(context.Background(), &walletv1.CreateWithdrawalDestinationRequest{
		TenantId: "tenant",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingFundingSourceID.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingFundingSourceID.Error())
	}
}

func TestCreateWithdrawalDestinationRequestCannotSubstituteAccountDetails(t *testing.T) {
	fields := (&walletv1.CreateWithdrawalDestinationRequest{}).ProtoReflect().Descriptor().Fields()
	if fields.ByName("destination_details") != nil || fields.ByName("wallet_id") != nil {
		t.Fatal("destination request exposes source-owned account fields")
	}
}

func TestFundingHandlersNormalizeBoundaryText(t *testing.T) {
	server, tenantID, ownedWallet, _ := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(42, tenantID)

	source, err := server.Service.Store.UpsertFundingSource(ctx, walletstore.FundingSource{
		TenantID:           tenantID,
		WalletID:           ownedWallet.ID,
		SourceType:         "bank_account",
		PSPProvider:        sql.NullString{String: "bankpay", Valid: true},
		ExternalReference:  sql.NullString{String: "boundary-text-test", Valid: true},
		VerificationStatus: walletstore.FundingSourceStatusVerified,
		VerifiedAt:         sql.NullTime{Time: time.Now().UTC(), Valid: true},
		Currency:           ownedWallet.Currency,
		SourceDetails:      []byte(`{"account_last4":"4321"}`),
		SupportsWithdrawal: true,
		WithdrawalMethod:   []byte(`{"account_number":"1234567890","bank_code":"044"}`),
	})
	if err != nil {
		t.Fatalf("UpsertFundingSource() error = %v", err)
	}

	sources, err := server.ListFundingSources(ctx, &walletv1.ListFundingSourcesRequest{
		TenantId: " " + tenantID + " ",
		WalletId: "\t" + ownedWallet.ID.String() + "\n",
	})
	if err != nil {
		t.Fatalf("ListFundingSources() error = %v", err)
	}
	if len(sources.GetSources()) != 1 || sources.GetSources()[0].GetId() != source.ID {
		t.Fatalf("ListFundingSources() = %+v, want source %d", sources.GetSources(), source.ID)
	}

	created, err := server.CreateWithdrawalDestination(ctx, &walletv1.CreateWithdrawalDestinationRequest{
		TenantId:              " " + tenantID + " ",
		LinkedFundingSourceId: source.ID,
		DisplayName:           "  Savings account  ",
		Country:               " \t ",
	})
	if err != nil {
		t.Fatalf("CreateWithdrawalDestination() error = %v", err)
	}
	if got := created.GetDestination().GetDisplayName(); got != "Savings account" {
		t.Fatalf("destination display name = %q, want %q", got, "Savings account")
	}
	if got := created.GetDestination().GetCountry(); got != "" {
		t.Fatalf("destination country = %q, want empty", got)
	}

	stored, err := server.Service.Store.GetWithdrawalDestination(ctx, tenantID, created.GetDestination().GetId())
	if err != nil {
		t.Fatalf("GetWithdrawalDestination() error = %v", err)
	}
	if !stored.DisplayName.Valid || stored.DisplayName.String != "Savings account" {
		t.Fatalf("stored display name = %+v, want valid trimmed value", stored.DisplayName)
	}
	if stored.Country.Valid || stored.Country.String != "" {
		t.Fatalf("stored country = %+v, want SQL NULL", stored.Country)
	}

	destinations, err := server.ListWithdrawalDestinations(ctx, &walletv1.ListWithdrawalDestinationsRequest{
		TenantId: " " + tenantID + " ",
		WalletId: "\t" + ownedWallet.ID.String() + "\n",
	})
	if err != nil {
		t.Fatalf("ListWithdrawalDestinations() error = %v", err)
	}
	if len(destinations.GetDestinations()) != 1 || destinations.GetDestinations()[0].GetId() != stored.ID {
		t.Fatalf("ListWithdrawalDestinations() = %+v, want destination %d", destinations.GetDestinations(), stored.ID)
	}
}
