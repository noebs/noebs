package psp

//go:generate go run github.com/golang/mock/mockgen@v1.6.0 -destination=mock/mock_provider.go -package=mock github.com/adonese/noebs/wallet/psp Provider

import "context"

type Operation string

const (
	OperationDeposit    Operation = "deposit"
	OperationWithdrawal Operation = "withdrawal"
)

type DepositRequest struct {
	ClientReference string
	IdempotencyKey  string
	Amount          int64
	Currency        string
	Metadata        map[string]any
}

type DepositResult struct {
	ClientReference string
	ProviderTxID    string
	Amount          int64
	Currency        string
	Status          string
	RawResponse     map[string]any
}

type PayoutRequest struct {
	ClientReference string
	IdempotencyKey  string
	Amount          int64
	Currency        string
	Destination     map[string]any
	Metadata        map[string]any
}

type PayoutResult struct {
	ProviderTxID string
	Amount       int64
	Currency     string
	Status       string
	RawResponse  map[string]any
}

type TxStatus struct {
	ProviderTxID string
	Amount       int64
	Currency     string
	Status       string
	RawResponse  map[string]any
}

type TransactionLookup struct {
	ProviderTxID    string
	IdempotencyKey  string
	ClientReference string
}

type Provider interface {
	CreateDeposit(ctx context.Context, req DepositRequest) (*DepositResult, error)
	SendPayout(ctx context.Context, req PayoutRequest) (*PayoutResult, error)
	GetTransactionStatus(ctx context.Context, lookup TransactionLookup) (*TxStatus, error)
	VerifyWebhook(payload []byte, signature string) bool
	Code() string
	SupportedOperations() []Operation
}
