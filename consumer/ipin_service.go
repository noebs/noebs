package consumer

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
)

func (s *Service) GenerateIpin(ctx context.Context, tenantID string, fields ebs_fields.ConsumerGenerateIPin) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}

	uid, _ := uuid.NewRandom()
	ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, s.NoebsConfig.EBSIPINPassword, uid.String())
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	req := fields
	req.Username = s.NoebsConfig.EBSIPINUsername
	req.Password = ipinBlock
	req.UUID = uid.String()
	if req.TranDateTime == "" {
		req.TranDateTime = ebs_fields.EbsDate()
	}

	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.IPINIp, ebs_fields.IPinGeneration, req)
}

func (s *Service) CompleteIpin(ctx context.Context, tenantID string, fields ebs_fields.ConsumerGenerateIPinCompletion) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}

	uid, _ := uuid.NewRandom()
	passwordBlock, err := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, s.NoebsConfig.EBSIPINPassword, uid.String())
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, fields.Ipin, uid.String())
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	otpBlock, err := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, fields.Otp, uid.String())
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	req := fields
	req.Password = passwordBlock
	req.Ipin = ipinBlock
	req.Otp = otpBlock
	req.UUID = uid.String()
	req.Username = s.NoebsConfig.EBSIPINUsername
	if req.TranDateTime == "" {
		req.TranDateTime = ebs_fields.EbsDate()
	}

	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.IPINIp, ebs_fields.IPinCompletion, req)
}

func (s *Service) IPINKey(ctx context.Context, tenantID string, fields ebs_fields.ConsumerGenerateIPINFields) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}

	req := fields
	if req.Username == "" {
		req.Username = s.NoebsConfig.EBSIPINUsername
	}
	if req.TranDateTime == "" {
		req.TranDateTime = ebs_fields.EbsDate()
	}
	if req.UUID == "" {
		id, _ := uuid.NewRandom()
		req.UUID = id.String()
	}

	return s.callEBSJSON(ctx, tenantID, s.NoebsConfig.IPINIp, ebs_fields.QRPublicKey, req)
}
