package merchant

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
)

// EBS-backed Merchant API operations.
//
// Handlers are responsible for validation and applying explicit config values.
// Service methods assume validated inputs.

func (s *Service) IsAlive(ctx context.Context, tenantID string, req ebs_fields.IsAliveFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.IsAliveEndpoint, req)
}

func (s *Service) WorkingKey(ctx context.Context, tenantID string, req ebs_fields.WorkingKeyFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.WorkingKeyEndpoint, req)
}

func (s *Service) Purchase(ctx context.Context, tenantID string, req ebs_fields.PurchaseFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.PurchaseEndpoint, req)
}

func (s *Service) Balance(ctx context.Context, tenantID string, req ebs_fields.BalanceFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.BalanceEndpoint, req)
}

func (s *Service) CardTransfer(ctx context.Context, tenantID string, req ebs_fields.CardTransferFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.CardTransferEndpoint, req)
}

func (s *Service) BillInquiry(ctx context.Context, tenantID string, req ebs_fields.BillInquiryFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.BillInquiryEndpoint, req)
}

func (s *Service) BillPayment(ctx context.Context, tenantID string, req ebs_fields.BillPaymentFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.BillPaymentEndpoint, req)
}

func (s *Service) TopUpPayment(ctx context.Context, tenantID string, req ebs_fields.BillPaymentFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.BillPrepaymentEndpoint, req)
}

func (s *Service) ChangePIN(ctx context.Context, tenantID string, req ebs_fields.ChangePINFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.ChangePINEndpoint, req)
}

func (s *Service) CashOut(ctx context.Context, tenantID string, req ebs_fields.CashOutFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.CashOutEndpoint, req)
}

func (s *Service) VoucherCashOut(ctx context.Context, tenantID string, req ebs_fields.VoucherCashOutFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.VoucherCashOutWithAmountEndpoint, req)
}

func (s *Service) VoucherCashIn(ctx context.Context, tenantID string, req ebs_fields.VoucherCashInFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.VoucherCashInEndpoint, req)
}

func (s *Service) Statement(ctx context.Context, tenantID string, req ebs_fields.MiniStatementFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.MiniStatementEndpoint, req)
}

func (s *Service) GenerateVoucher(ctx context.Context, tenantID string, req ebs_fields.GenerateVoucherFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.GenerateVoucherEndpoint, req)
}

func (s *Service) CashIn(ctx context.Context, tenantID string, req ebs_fields.CashInFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.CashInEndpoint, req)
}

func (s *Service) ToAccount(ctx context.Context, tenantID string, req ebs_fields.AccountTransferFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.AccountTransferEndpoint, req)
}

func (s *Service) MiniStatement(ctx context.Context, tenantID string, req ebs_fields.MiniStatementFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.MiniStatementEndpoint, req)
}

func (s *Service) Refund(ctx context.Context, tenantID string, req ebs_fields.RefundFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, ebs_fields.RefundEndpoint, req)
}

// Proxy forwards an arbitrary endpoint + payload to the upstream merchant EBS service.
//
// endpoint is appended to the configured merchant base URL.
func (s *Service) Proxy(ctx context.Context, tenantID, endpoint string, payload []byte) (ebs_fields.EBSParserFields, error) {
	return s.callEBSRaw(ctx, tenantID, endpoint, payload)
}
