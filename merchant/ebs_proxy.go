package merchant

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/sirupsen/logrus"
)

var (
	ErrMissingService        = errors.New("missing merchant service")
	ErrMissingStore          = errors.New("missing merchant store")
	ErrMissingHTTPClient     = errors.New("missing_http_client")
	ErrMissingAdminReporting = errors.New("missing_admin_reporting_service_discovery")
	ErrInvalidAdminReporting = errors.New("invalid_admin_reporting_service_discovery")
	ErrAdminReportingCommand = errors.New("admin_reporting_command_failed")
)

func (s *Service) callEBSJSON(ctx context.Context, tenantID, endpoint string, req any) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}
	if err := s.requireTransactionProjectionTarget(); err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	url := s.NoebsConfig.MerchantIP + endpoint
	payload, err := json.Marshal(req)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, payload)
	return s.finalizeEBSCall(ctx, tenantID, endpoint, code, res, ebsErr)
}

func (s *Service) callEBSRaw(ctx context.Context, tenantID, endpoint string, payload []byte) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}
	if err := s.requireTransactionProjectionTarget(); err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	url := s.NoebsConfig.MerchantIP + endpoint
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, payload)
	return s.finalizeEBSCall(ctx, tenantID, endpoint, code, res, ebsErr)
}

func (s *Service) finalizeEBSCall(ctx context.Context, tenantID, endpoint string, code int, res ebs_fields.EBSParserFields, ebsErr error) (ebs_fields.EBSParserFields, error) {
	res.MaskPAN()
	recordErr := s.recordTransaction(ctx, tenantID, res.EBSResponse)
	if recordErr != nil {
		if s.Logger != nil {
			s.Logger.WithFields(logrus.Fields{
				"tenant_id": tenantID,
				"endpoint":  endpoint,
				"error":     recordErr,
			}).Warn("record transaction failed")
		}
	}
	if ebsErr != nil {
		return res, errors.Join(&ebs_fields.CallError{Status: code, Response: res, Err: ebsErr}, recordErr)
	}
	if recordErr != nil {
		return res, recordErr
	}
	return res, nil
}
