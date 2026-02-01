package mock

import (
	"context"

	"github.com/adonese/noebs/wallet/psp"
)

type Provider struct {
	CodeValue      string
	Operations     []psp.Operation
	VerifyResult   *psp.DepositVerification
	VerifyErr      error
	PayoutResult   *psp.PayoutResult
	PayoutErr      error
	StatusResult   *psp.TxStatus
	StatusErr      error
	WebhookAllowed bool
}

func (p *Provider) VerifyDeposit(ctx context.Context, txID string) (*psp.DepositVerification, error) {
	_ = ctx
	_ = txID
	return p.VerifyResult, p.VerifyErr
}

func (p *Provider) SendPayout(ctx context.Context, req psp.PayoutRequest) (*psp.PayoutResult, error) {
	_ = ctx
	_ = req
	return p.PayoutResult, p.PayoutErr
}

func (p *Provider) GetTransactionStatus(ctx context.Context, txID string) (*psp.TxStatus, error) {
	_ = ctx
	_ = txID
	return p.StatusResult, p.StatusErr
}

func (p *Provider) VerifyWebhook(payload []byte, signature string) bool {
	_ = payload
	_ = signature
	return p.WebhookAllowed
}

func (p *Provider) Code() string {
	return p.CodeValue
}

func (p *Provider) SupportedOperations() []psp.Operation {
	return append([]psp.Operation(nil), p.Operations...)
}
