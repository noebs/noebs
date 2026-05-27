package consumer

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/sirupsen/logrus"
)

var ErrMissingService = errors.New("missing consumer service")

func (s *Service) callEBSJSON(ctx context.Context, tenantID, baseURL, endpoint string, req any) (ebs_fields.EBSParserFields, error) {
	return s.callEBSJSONWithMutate(ctx, tenantID, baseURL, endpoint, req, nil)
}

func (s *Service) callEBSJSONWithMutate(ctx context.Context, tenantID, baseURL, endpoint string, req any, mutate func(*ebs_fields.EBSParserFields)) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}
	if err := s.requireTransactionProjectionTarget(); err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	url := baseURL + endpoint
	payload, err := json.Marshal(req)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, payload)
	return s.finalizeEBSCall(ctx, tenantID, url, endpoint, code, res, ebsErr, mutate)
}

func (s *Service) callEBSRaw(ctx context.Context, tenantID, baseURL, endpoint string, payload []byte) (ebs_fields.EBSParserFields, error) {
	return s.callEBSRawWithMutate(ctx, tenantID, baseURL, endpoint, payload, nil)
}

func (s *Service) callEBSRawWithMutate(ctx context.Context, tenantID, baseURL, endpoint string, payload []byte, mutate func(*ebs_fields.EBSParserFields)) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}
	if err := s.requireTransactionProjectionTarget(); err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	url := baseURL + endpoint
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, payload)
	return s.finalizeEBSCall(ctx, tenantID, url, endpoint, code, res, ebsErr, mutate)
}

func (s *Service) finalizeEBSCall(ctx context.Context, tenantID, url, endpoint string, code int, res ebs_fields.EBSParserFields, ebsErr error, mutate func(*ebs_fields.EBSParserFields)) (ebs_fields.EBSParserFields, error) {
	res.Name = s.ToDatabasename(url)

	if mutate != nil {
		// mutate must not assume masked PAN values.
		mutate(&res)
	}

	// Always mask before returning (store also masks before persisting).
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
