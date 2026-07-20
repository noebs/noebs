package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/transactionauth"
	walletrequest "github.com/adonese/noebs/wallet/request"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func TestGRPCUserTransactionHandlersForwardCanonicalRequests(t *testing.T) {
	defaults := walletrequest.Defaults{
		HoldExpirySeconds:      3600,
		ApprovalTimeoutSeconds: 7200,
		ApprovalThreshold:      100000,
	}
	client := &transactionCaptureClient{}
	handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/wallet/p2p", gateway.InternalUserIdentityMiddleware(), handler.RequestP2PTransfer)
	app.Post("/wallet/withdrawals", gateway.InternalUserIdentityMiddleware(), handler.RequestWithdrawal)

	t.Run("p2p", func(t *testing.T) {
		publicBody := []byte(`{
			"currency":" SDG ",
			"from_wallet_id":"550e8400-e29b-41d4-a716-446655440000",
			"to_wallet_id":"550e8400-e29b-41d4-a716-446655440001",
			"amount":100,
			"description":" transfer ",
			"idempotency_key":"handler-p2p",
			"reference_id":"handler-p2p",
			"to_owner_type":"user",
			"to_owner_id":"7"
		}`)
		canonical, err := walletrequest.ParsePublic(transactionauth.OperationWalletP2P, "tenant-1", publicBody, defaults)
		if err != nil {
			t.Fatal(err)
		}
		response := performCanonicalWalletTransaction(t, app, "/wallet/p2p", canonical.Body)
		if response.StatusCode != http.StatusAccepted {
			closeWalletResponseBody(t, response.Body)
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
		}
		closeWalletResponseBody(t, response.Body)
		want := canonical.Message.(*walletv1.RequestP2PTransferRequest)
		if client.p2p == nil || !proto.Equal(client.p2p, want) {
			t.Fatalf("gRPC request = %+v, want %+v", client.p2p, want)
		}
	})

	t.Run("withdrawal with present false policy", func(t *testing.T) {
		publicBody := []byte(`{
			"client_reference":"handler-withdrawal",
			"provider_code":"bank",
			"wallet_id":"550e8400-e29b-41d4-a716-446655440000",
			"amount":1,
			"currency":"SDG",
			"allow_return_to_source":true,
			"idempotency_key":"handler-withdrawal"
		}`)
		canonical, err := walletrequest.ParsePublic(transactionauth.OperationWalletWithdrawal, "tenant-1", publicBody, defaults)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(canonical.Body, []byte(`"approval_required":false`)) {
			t.Fatalf("canonical body omitted false approval policy: %s", canonical.Body)
		}
		response := performCanonicalWalletTransaction(t, app, "/wallet/withdrawals", canonical.Body)
		if response.StatusCode != http.StatusAccepted {
			closeWalletResponseBody(t, response.Body)
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
		}
		closeWalletResponseBody(t, response.Body)
		want := canonical.Message.(*walletv1.RequestWithdrawalRequest)
		if client.withdrawal == nil || client.withdrawal.ApprovalRequired == nil ||
			client.withdrawal.GetApprovalRequired() || !proto.Equal(client.withdrawal, want) {
			t.Fatalf("gRPC request = %+v, want present-false policy in %+v", client.withdrawal, want)
		}
	})
}

func performCanonicalWalletTransaction(t testing.TB, app *fiber.App, path string, body []byte) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	setWalletPrincipalHeaders(request, walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour)))
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type transactionCaptureClient struct {
	walletv1.WalletPublicServiceClient
	p2p        *walletv1.RequestP2PTransferRequest
	withdrawal *walletv1.RequestWithdrawalRequest
}

func (c *transactionCaptureClient) RequestP2PTransfer(
	_ context.Context,
	request *walletv1.RequestP2PTransferRequest,
	_ ...grpc.CallOption,
) (*walletv1.RequestP2PTransferResponse, error) {
	c.p2p = proto.Clone(request).(*walletv1.RequestP2PTransferRequest)
	return &walletv1.RequestP2PTransferResponse{WorkflowId: "p2p-workflow", RunId: "p2p-run"}, nil
}

func (c *transactionCaptureClient) RequestWithdrawal(
	_ context.Context,
	request *walletv1.RequestWithdrawalRequest,
	_ ...grpc.CallOption,
) (*walletv1.RequestWithdrawalResponse, error) {
	c.withdrawal = proto.Clone(request).(*walletv1.RequestWithdrawalRequest)
	return &walletv1.RequestWithdrawalResponse{WorkflowId: "withdrawal-workflow", RunId: "withdrawal-run"}, nil
}
