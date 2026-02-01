package noop

import (
	"context"

	"github.com/adonese/noebs/wallet/psp"
)

type Provider struct{}

func (p *Provider) VerifyDeposit(ctx context.Context, txID string) (*psp.DepositVerification, error) {
	_ = ctx
	_ = txID
	return nil, psp.ErrPSPPermanent
}

func (p *Provider) SendPayout(ctx context.Context, req psp.PayoutRequest) (*psp.PayoutResult, error) {
	_ = ctx
	_ = req
	return nil, psp.ErrPSPPermanent
}

func (p *Provider) GetTransactionStatus(ctx context.Context, txID string) (*psp.TxStatus, error) {
	_ = ctx
	_ = txID
	return nil, psp.ErrPSPPermanent
}

func (p *Provider) VerifyWebhook(payload []byte, signature string) bool {
	_ = payload
	_ = signature
	return false
}

func (p *Provider) Code() string {
	return "noop"
}

func (p *Provider) SupportedOperations() []psp.Operation {
	return []psp.Operation{psp.OperationDeposit, psp.OperationWithdrawal}
}
