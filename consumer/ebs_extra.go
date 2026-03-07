package consumer

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
)

func (s *Service) storeLastTransactions(ctx context.Context, merchantID string, res *ebs_fields.EBSParserFields) error {
	_ = ctx
	_ = merchantID
	_ = res
	return nil
}

func (s *Service) QRTransactions(ctx context.Context, tenantID string, req ebs_fields.ConsumerQRStatus) (ebs_fields.EBSParserFields, error) {
	res, err := s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.MerchantTransactionStatus, req)
	if err == nil {
		_ = s.storeLastTransactions(ctx, req.MerchantID, &res)
	}
	return res, err
}
