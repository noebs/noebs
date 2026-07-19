package store

import (
	"context"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

func (s *Store) requireDataKeyForSensitiveValue(values ...string) error {
	if s == nil || s.crypto != nil {
		return nil
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !strings.HasPrefix(value, hashPrefix) && !strings.HasPrefix(value, encPrefix) {
			return ErrMissingDataKey
		}
	}
	return nil
}

func (s *Store) encryptTokenFields(token *ebs_fields.Token) (string, string, error) {
	if s.crypto == nil || token == nil || token.ToCard == "" {
		return token.ToCard, "", nil
	}
	enc, err := s.crypto.Encrypt(token.ToCard)
	if err != nil {
		return "", "", err
	}
	return s.crypto.Hash(token.ToCard), enc, nil
}

func (s *Store) hydrateTokenFields(ctx context.Context, tenantID string, token *ebs_fields.Token) error {
	if s.crypto == nil || token == nil {
		return nil
	}
	if token.ToCardEnc != "" {
		pan, err := s.crypto.Decrypt(token.ToCardEnc)
		if err != nil {
			return err
		}
		token.ToCard = pan
		return nil
	}
	if !looksLikePAN(token.ToCard) {
		return nil
	}
	enc, err := s.crypto.Encrypt(token.ToCard)
	if err != nil {
		return err
	}
	if token.ID == 0 {
		return nil
	}
	hash := s.crypto.Hash(token.ToCard)
	if err := s.updateTokenCard(ctx, tenantID, token.UUID, hash, enc); err != nil {
		return err
	}
	pan, err := s.crypto.Decrypt(enc)
	if err != nil {
		return err
	}
	token.ToCardEnc = enc
	token.ToCard = pan
	return nil
}

func (s *Store) updateTokenCard(ctx context.Context, tenantID, uuid, hash, enc string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return ErrMissingUUID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE tokens SET to_card = ?, to_card_enc = ?, updated_at = ? WHERE tenant_id = ? AND uuid = ? AND payment_status = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, hash, enc, time.Now().UTC(), tenantID, uuid, ebs_fields.PaymentTokenStatusAvailable)
}
