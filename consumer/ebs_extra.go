package consumer

import (
	"context"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func (s *Service) storeLastTransactions(ctx context.Context, tenantID, merchantID string, res *ebs_fields.EBSParserFields) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if res == nil || len(res.LastTransactions) == 0 {
		return nil
	}
	for _, purchase := range res.LastTransactions {
		txn, err := qrPurchaseTransaction(merchantID, purchase)
		if err != nil {
			return err
		}
		if err := s.recordTransaction(ctx, tenantID, txn); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) QRTransactions(ctx context.Context, tenantID string, req ebs_fields.ConsumerQRStatus) (ebs_fields.EBSParserFields, error) {
	res, err := s.callEBSJSONWithoutTransactionRecord(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.MerchantTransactionStatus, req)
	if err != nil {
		return res, err
	}
	if err := s.storeLastTransactions(ctx, tenantID, req.MerchantID, &res); err != nil {
		return res, err
	}
	return res, nil
}

func qrPurchaseTransaction(requestMerchantID string, purchase ebs_fields.QRPurchase) (ebs_fields.EBSResponse, error) {
	requestMerchantID = strings.TrimSpace(requestMerchantID)
	if requestMerchantID == "" {
		return ebs_fields.EBSResponse{}, ErrMissingMerchantID
	}
	uuid := strings.TrimSpace(purchase.UUID)
	if uuid == "" {
		return ebs_fields.EBSResponse{}, store.ErrMissingUUID
	}
	merchantID := strings.TrimSpace(purchase.MerchantID)
	if merchantID == "" {
		merchantID = requestMerchantID
	}
	if merchantID != requestMerchantID {
		return ebs_fields.EBSResponse{}, ErrInvalidMerchantID
	}
	return ebs_fields.EBSResponse{
		UUID:                     uuid,
		MerchantID:               merchantID,
		MerchantName:             purchase.MerchantName,
		MerchantCity:             purchase.MerchantCity,
		MerchantAccountReference: purchase.MerchantAccountReference,
		MerchantAccountType:      purchase.MerchantAccountType,
		MobileNo:                 purchase.MerchantMobileNo,
		PAN:                      purchase.Pan,
		ResponseCode:             int(purchase.ResponseCode),
		ResponseMessage:          purchase.ResponseMessage,
		ResponseStatus:           purchase.ResponseStatus,
		TranAmount:               float32(purchase.TranAmount),
		TranDateTime:             purchase.TranDateTime,
		TransactionID:            purchase.TransactionID,
		AuthenticationType:       purchase.AuthenticationType,
	}, nil
}
