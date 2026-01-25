package store

import (
	"context"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

func (s *Store) encryptUserFields(user *ebs_fields.User) {
	if s.crypto == nil || user == nil {
		return
	}
	if user.MainCard != "" && !s.crypto.IsHash(user.MainCard) {
		user.MainCardEnc, _ = s.crypto.Encrypt(user.MainCard)
		user.MainCard = s.crypto.Hash(user.MainCard)
	}
}

func (s *Store) hydrateUserFields(ctx context.Context, tenantID string, user *ebs_fields.User) {
	if s.crypto == nil || user == nil {
		return
	}
	if user.MainCardEnc != "" {
		if pan, err := s.crypto.Decrypt(user.MainCardEnc); err == nil {
			user.MainCard = pan
		}
		return
	}
	if looksLikePAN(user.MainCard) {
		enc, err := s.crypto.Encrypt(user.MainCard)
		if err != nil || user.ID == 0 {
			return
		}
		hash := s.crypto.Hash(user.MainCard)
		user.MainCardEnc = enc
		user.MainCard = hash
		_ = s.updateUserMainCard(ctx, tenantID, user.ID, hash, enc)
		if pan, err := s.crypto.Decrypt(enc); err == nil {
			user.MainCard = pan
		}
	}
}

func (s *Store) updateUserMainCard(ctx context.Context, tenantID string, userID int64, hash, enc string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE users SET main_card = ?, main_card_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	_, err = db.ExecContext(ctx, stmt, hash, enc, time.Now().UTC(), tenantID, userID)
	return err
}

func (s *Store) encryptCardFields(card *ebs_fields.Card) {
	if s.crypto == nil || card == nil {
		return
	}
	if card.Pan != "" && !s.crypto.IsHash(card.Pan) {
		card.PanEnc, _ = s.crypto.Encrypt(card.Pan)
		card.Pan = s.crypto.Hash(card.Pan)
	}
	if card.IPIN != "" && !s.crypto.IsEncrypted(card.IPIN) {
		card.IPINEnc, _ = s.crypto.Encrypt(card.IPIN)
		card.IPIN = ""
	}
}

func (s *Store) hydrateCardFields(ctx context.Context, tenantID string, card *ebs_fields.Card) {
	if s.crypto == nil || card == nil {
		return
	}
	if card.PanEnc != "" {
		if pan, err := s.crypto.Decrypt(card.PanEnc); err == nil {
			card.Pan = pan
		}
	} else if looksLikePAN(card.Pan) {
		enc, err := s.crypto.Encrypt(card.Pan)
		if err == nil && card.ID != 0 {
			hash := s.crypto.Hash(card.Pan)
			card.PanEnc = enc
			card.Pan = hash
			_ = s.updateCardPan(ctx, tenantID, card.ID, hash, enc)
			if pan, err := s.crypto.Decrypt(enc); err == nil {
				card.Pan = pan
			}
		}
	}
	if card.IPINEnc != "" {
		if pin, err := s.crypto.Decrypt(card.IPINEnc); err == nil {
			card.IPIN = pin
		}
	} else if card.IPIN != "" {
		enc, err := s.crypto.Encrypt(card.IPIN)
		if err == nil && card.ID != 0 {
			card.IPINEnc = enc
			card.IPIN = ""
			_ = s.updateCardIPIN(ctx, tenantID, card.ID, enc)
			if pin, err := s.crypto.Decrypt(enc); err == nil {
				card.IPIN = pin
			}
		}
	}
}

func (s *Store) updateCardPan(ctx context.Context, tenantID string, cardID int64, hash, enc string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE cards SET pan = ?, pan_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	_, err = db.ExecContext(ctx, stmt, hash, enc, time.Now().UTC(), tenantID, cardID)
	return err
}

func (s *Store) updateCardIPIN(ctx context.Context, tenantID string, cardID int64, enc string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE cards SET ipin = '', ipin_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	_, err = db.ExecContext(ctx, stmt, enc, time.Now().UTC(), tenantID, cardID)
	return err
}

func (s *Store) encryptCacheCardFields(card *ebs_fields.CacheCards) {
	if s.crypto == nil || card == nil {
		return
	}
	if card.Pan != "" && !s.crypto.IsHash(card.Pan) {
		card.PanEnc, _ = s.crypto.Encrypt(card.Pan)
		card.Pan = s.crypto.Hash(card.Pan)
	}
}

func (s *Store) hydrateCacheCardFields(ctx context.Context, tenantID string, card *ebs_fields.CacheCards) {
	if s.crypto == nil || card == nil {
		return
	}
	if card.PanEnc != "" {
		if pan, err := s.crypto.Decrypt(card.PanEnc); err == nil {
			card.Pan = pan
		}
	} else if looksLikePAN(card.Pan) {
		enc, err := s.crypto.Encrypt(card.Pan)
		if err == nil && card.ID != 0 {
			hash := s.crypto.Hash(card.Pan)
			card.PanEnc = enc
			card.Pan = hash
			_ = s.updateCacheCardPan(ctx, tenantID, card.ID, hash, enc)
			if pan, err := s.crypto.Decrypt(enc); err == nil {
				card.Pan = pan
			}
		}
	}
}

func (s *Store) updateCacheCardPan(ctx context.Context, tenantID string, cardID int64, hash, enc string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE cache_cards SET pan = ?, pan_enc = ?, updated_at = ? WHERE tenant_id = ? AND id = ?")
	_, err = db.ExecContext(ctx, stmt, hash, enc, time.Now().UTC(), tenantID, cardID)
	return err
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

func (s *Store) hydrateTokenFields(ctx context.Context, tenantID string, token *ebs_fields.Token) {
	if s.crypto == nil || token == nil {
		return
	}
	if token.ToCardEnc != "" {
		if pan, err := s.crypto.Decrypt(token.ToCardEnc); err == nil {
			token.ToCard = pan
		}
		return
	}
	if looksLikePAN(token.ToCard) {
		enc, err := s.crypto.Encrypt(token.ToCard)
		if err == nil && token.ID != 0 {
			hash := s.crypto.Hash(token.ToCard)
			token.ToCardEnc = enc
			token.ToCard = hash
			_ = s.updateTokenCard(ctx, tenantID, token.UUID, hash, enc)
			if pan, err := s.crypto.Decrypt(enc); err == nil {
				token.ToCard = pan
			}
		}
	}
}

func (s *Store) updateTokenCard(ctx context.Context, tenantID, uuid, hash, enc string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE tokens SET to_card = ?, to_card_enc = ?, updated_at = ? WHERE tenant_id = ? AND uuid = ?")
	_, err = db.ExecContext(ctx, stmt, hash, enc, time.Now().UTC(), tenantID, uuid)
	return err
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
