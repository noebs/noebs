package consumer

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

type CheckUserResult struct {
	Phone  string `json:"phone"`
	IsUser bool   `json:"is_user"`
}

const maxCheckUserPhones = 50

func (s *Service) CheckUser(ctx context.Context, tenantID string, requesterID int64, phones []string, source string, now time.Time) ([]CheckUserResult, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if requesterID <= 0 {
		return nil, store.ErrInvalidUserID
	}
	phones, err = normalizeCheckUserPhones(phones)
	if err != nil {
		return nil, err
	}
	source, err = normalizeRequestSource(source)
	if err != nil {
		return nil, err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		sourceLimit("check-user-source", source, 60, 15*time.Minute),
		authLimitRule{
			action:  "check-user-account",
			subject: authSubjectHash("user_id", strconv.FormatInt(requesterID, 10)),
			limit:   20,
			window:  15 * time.Minute,
		},
	); err != nil {
		return nil, err
	}

	out := make([]CheckUserResult, 0, len(phones))
	for _, phone := range phones {
		_, err := s.Store.GetUserByMobile(ctx, tenantID, phone)
		if err != nil {
			if !store.ErrNotFound(err) {
				return nil, err
			}
			out = append(out, CheckUserResult{Phone: phone, IsUser: false})
			continue
		}

		out = append(out, CheckUserResult{Phone: phone, IsUser: true})
	}
	return out, nil
}

func normalizeCheckUserPhones(phones []string) ([]string, error) {
	if len(phones) == 0 {
		return nil, ErrMissingMobile
	}
	if len(phones) > maxCheckUserPhones {
		return nil, ErrCheckUserBatchTooLarge
	}
	normalized := make([]string, 0, len(phones))
	seen := make(map[string]struct{}, len(phones))
	for _, phone := range phones {
		phone = strings.TrimSpace(phone)
		if phone == "" {
			return nil, ErrMissingMobile
		}
		if _, exists := seen[phone]; exists {
			continue
		}
		seen[phone] = struct{}{}
		normalized = append(normalized, phone)
	}
	return normalized, nil
}

func (s *Service) SetMainCardForUserID(ctx context.Context, tenantID string, userID int64, pan string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	pan = strings.TrimSpace(pan)
	if pan == "" {
		return store.ErrMissingPAN
	}
	if ok, err := s.Store.CardExists(ctx, tenantID, pan); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrCardNotFound
	}
	return s.Store.SetMainCard(ctx, tenantID, userID, pan)
}

func (s *Service) GetTransactionsForUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.EBSResponse, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, store.ErrInvalidUserID
	}

	return s.Store.GetTransactionsByParticipantUserID(ctx, tenantID, userID)
}
