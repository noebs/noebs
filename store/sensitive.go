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
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, hashPrefix) || strings.HasPrefix(value, encPrefix) {
			continue
		}
		if value != "" {
			return ErrMissingDataKey
		}
	}
	return nil
}

func (s *Store) encryptUserFields(user *ebs_fields.User) error {
	if s.crypto == nil || user == nil {
		return nil
	}
	if user.MainCard != "" && !s.crypto.IsHash(user.MainCard) {
		enc, err := s.crypto.Encrypt(user.MainCard)
		if err != nil {
			return err
		}
		user.MainCardEnc = enc
		user.MainCard = s.crypto.Hash(user.MainCard)
	}
	return nil
}

func (s *Store) hydrateUserFields(ctx context.Context, tenantID string, user *ebs_fields.User) error {
	if s.crypto == nil || user == nil {
		return nil
	}
	if user.MainCardEnc != "" {
		pan, err := s.crypto.Decrypt(user.MainCardEnc)
		if err != nil {
			return err
		}
		user.MainCard = pan
		return nil
	}
	if looksLikePAN(user.MainCard) {
		enc, err := s.crypto.Encrypt(user.MainCard)
		if err != nil || user.ID == 0 {
			return err
		}
		hash := s.crypto.Hash(user.MainCard)
		if err := s.updateUserMainCard(ctx, tenantID, user.ID, hash, enc); err != nil {
			return err
		}
		pan, err := s.crypto.Decrypt(enc)
		if err != nil {
			return err
		}
		user.MainCardEnc = enc
		user.MainCard = pan
	}
	return nil
}

func (s *Store) updateUserMainCard(ctx context.Context, tenantID string, userID int64, hash, enc string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE users SET main_card = ?, main_card_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, hash, enc, time.Now().UTC(), tenantID, userID)
}

func (s *Store) encryptCardFields(card *ebs_fields.Card) error {
	if s.crypto == nil || card == nil {
		return nil
	}
	pan := card.Pan
	panEnc := card.PanEnc
	ipin := card.IPIN
	ipinEnc := card.IPINEnc
	if card.Pan != "" && !s.crypto.IsHash(card.Pan) {
		enc, err := s.crypto.Encrypt(card.Pan)
		if err != nil {
			return err
		}
		panEnc = enc
		pan = s.crypto.Hash(card.Pan)
	}
	if card.IPIN != "" && !s.crypto.IsEncrypted(card.IPIN) {
		enc, err := s.crypto.Encrypt(card.IPIN)
		if err != nil {
			return err
		}
		ipinEnc = enc
		ipin = ""
	}
	card.Pan = pan
	card.PanEnc = panEnc
	card.IPIN = ipin
	card.IPINEnc = ipinEnc
	return nil
}

func (s *Store) hydrateCardFields(ctx context.Context, tenantID string, card *ebs_fields.Card) error {
	if s.crypto == nil || card == nil {
		return nil
	}
	if card.PanEnc != "" {
		pan, err := s.crypto.Decrypt(card.PanEnc)
		if err != nil {
			return err
		}
		card.Pan = pan
	} else if looksLikePAN(card.Pan) {
		enc, err := s.crypto.Encrypt(card.Pan)
		if err == nil && card.ID != 0 {
			hash := s.crypto.Hash(card.Pan)
			if err := s.updateCardPan(ctx, tenantID, card.ID, hash, enc); err != nil {
				return err
			}
			pan, err := s.crypto.Decrypt(enc)
			if err != nil {
				return err
			}
			card.PanEnc = enc
			card.Pan = pan
		} else if err != nil {
			return err
		}
	}
	if card.IPINEnc != "" {
		pin, err := s.crypto.Decrypt(card.IPINEnc)
		if err != nil {
			return err
		}
		card.IPIN = pin
	} else if card.IPIN != "" {
		enc, err := s.crypto.Encrypt(card.IPIN)
		if err == nil && card.ID != 0 {
			if err := s.updateCardIPIN(ctx, tenantID, card.ID, enc); err != nil {
				return err
			}
			pin, err := s.crypto.Decrypt(enc)
			if err != nil {
				return err
			}
			card.IPINEnc = enc
			card.IPIN = pin
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) updateCardPan(ctx context.Context, tenantID string, cardID int64, hash, enc string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if cardID <= 0 {
		return ErrInvalidCardID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE cards SET pan = ?, pan_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, hash, enc, time.Now().UTC(), tenantID, cardID)
}

func (s *Store) updateCardIPIN(ctx context.Context, tenantID string, cardID int64, enc string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if cardID <= 0 {
		return ErrInvalidCardID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE cards SET ipin = '', ipin_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, enc, time.Now().UTC(), tenantID, cardID)
}

func (s *Store) encryptCacheCardFields(card *ebs_fields.CacheCards) error {
	if s.crypto == nil || card == nil {
		return nil
	}
	if card.Pan != "" && !s.crypto.IsHash(card.Pan) {
		enc, err := s.crypto.Encrypt(card.Pan)
		if err != nil {
			return err
		}
		card.PanEnc = enc
		card.Pan = s.crypto.Hash(card.Pan)
	}
	return nil
}

func (s *Store) hydrateCacheCardFields(ctx context.Context, tenantID string, card *ebs_fields.CacheCards) error {
	if s.crypto == nil || card == nil {
		return nil
	}
	if card.PanEnc != "" {
		pan, err := s.crypto.Decrypt(card.PanEnc)
		if err != nil {
			return err
		}
		card.Pan = pan
	} else if looksLikePAN(card.Pan) {
		enc, err := s.crypto.Encrypt(card.Pan)
		if err == nil && card.ID != 0 {
			hash := s.crypto.Hash(card.Pan)
			if err := s.updateCacheCardPan(ctx, tenantID, card.ID, hash, enc); err != nil {
				return err
			}
			pan, err := s.crypto.Decrypt(enc)
			if err != nil {
				return err
			}
			card.PanEnc = enc
			card.Pan = pan
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) updateCacheCardPan(ctx context.Context, tenantID string, cardID int64, hash, enc string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if cardID <= 0 {
		return ErrInvalidCardID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE cache_cards SET pan = ?, pan_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, hash, enc, time.Now().UTC(), tenantID, cardID)
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
	if looksLikePAN(token.ToCard) {
		enc, err := s.crypto.Encrypt(token.ToCard)
		if err == nil && token.ID != 0 {
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
		} else if err != nil {
			return err
		}
	}
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

func (s *Store) panLookupArgs(pan string) []any {
	if s.crypto == nil {
		return []any{pan}
	}
	return []any{s.crypto.Hash(pan), pan}
}

func (s *Store) panLookupClause(column string) string {
	if s.crypto == nil {
		return column + " = ?"
	}
	return "(" + column + " = ? OR " + column + " = ?)"
}
