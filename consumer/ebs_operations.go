package consumer

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
)

// EBS-backed Consumer API operations.
//
// Handlers are responsible for validation + applying config-driven defaults
// (e.g., ApplicationId, dynamic fees). Service methods assume validated inputs.

func (s *Service) Purchase(ctx context.Context, tenantID string, req ebs_fields.ConsumerPurchaseFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerPurchaseEndpoint, req)
}

func (s *Service) IsAlive(ctx context.Context, tenantID string, req ebs_fields.ConsumerIsAliveFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerIsAliveEndpoint, req)
}

func (s *Service) BillInquiry(ctx context.Context, tenantID string, req ebs_fields.ConsumerBillInquiryFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerBillInquiryEndpoint, req)
}

func (s *Service) Balance(ctx context.Context, tenantID string, req ebs_fields.ConsumerBalanceFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerBalanceEndpoint, req)
}

func (s *Service) TransactionStatus(ctx context.Context, tenantID string, req ebs_fields.ConsumerTransactionStatusFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerTransactionStatusEndpoint, req)
}

func (s *Service) WorkingKey(ctx context.Context, tenantID string, req ebs_fields.ConsumerWorkingKeyFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerWorkingKeyEndpoint, req)
}

func (s *Service) CashIn(ctx context.Context, tenantID string, req ebs_fields.ConsumerCashInFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerCashInEndpoint, req)
}

func (s *Service) CashOut(ctx context.Context, tenantID string, req ebs_fields.ConsumerCashoOutFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerCashOutEndpoint, req)
}

func (s *Service) AccountTransfer(ctx context.Context, tenantID string, req ebs_fields.ConsumrAccountTransferFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerAccountTransferEndpoint, req)
}

func (s *Service) IPinChange(ctx context.Context, tenantID string, req ebs_fields.ConsumerIPinFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerChangeIPinEndpoint, req)
}

func (s *Service) Status(ctx context.Context, tenantID string, req ebs_fields.ConsumerStatusFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerStatusEndpoint, req)
}

func (s *Service) QRMerchantRegistration(ctx context.Context, tenantID string, req ebs_fields.ConsumerQRRegistration) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerQRGenerationEndpoint, req)
}

func (s *Service) QRPayment(ctx context.Context, tenantID string, req ebs_fields.ConsumerQRPaymentFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerQRPaymentEndpoint, req)
}

func (s *Service) QRRefund(ctx context.Context, tenantID string, req ebs_fields.ConsumerQRRefundFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerQRRefundEndpoint, req)
}

func (s *Service) QRComplete(ctx context.Context, tenantID string, req ebs_fields.ConsumerQRCompleteFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerComplete, req)
}

func (s *Service) QRGeneration(ctx context.Context, tenantID string, req ebs_fields.MerchantRegistrationFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerQRGenerationEndpoint, req)
}

func (s *Service) EbsGetCardInfo(ctx context.Context, tenantID string, req ebs_fields.ConsumerCardInfoFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerCardInfo, req)
}

func (s *Service) GetMSISDNFromCard(ctx context.Context, tenantID string, req ebs_fields.ConsumerPANFromMobileFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerPANFromMobile, req)
}

func (s *Service) RegisterCard(ctx context.Context, tenantID string, req ebs_fields.ConsumerRegistrationFields) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerRegister, req)
}
