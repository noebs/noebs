package consumer

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

const (
	cardEnrollmentIntentTTL       = 5 * time.Minute
	cardEnrollmentVerification    = "ebs_balance_v1"
	cardEnrollmentPublicAlgorithm = "rsa_pkcs1_v1_5"
)

type CardEnrollmentKeyMetadata struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

type CardEnrollmentIntentResponse struct {
	EnrollmentID string                    `json:"enrollment_id"`
	RailUUID     string                    `json:"rail_uuid"`
	ExpiresAt    time.Time                 `json:"expires_at"`
	RailKey      CardEnrollmentKeyMetadata `json:"rail_key"`
}

type ConfirmCardEnrollmentRequest struct {
	RailUUID  string `json:"rail_uuid"`
	PAN       string `json:"pan"`
	Expiry    string `json:"exp_date"`
	Name      string `json:"name"`
	IPINBlock string `json:"ipin_block"`
}

type CreateCardEnrollmentIntentCommand struct{}

type CardEnrollmentIntentCommandResult struct {
	EnrollmentID string    `json:"enrollment_id"`
	RailUUID     string    `json:"rail_uuid"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type BeginCardEnrollmentCommand struct {
	EnrollmentID string `json:"enrollment_id"`
	PAN          string `json:"pan"`
	Expiry       string `json:"exp_date"`
	Name         string `json:"name"`
}

type BeginCardEnrollmentResult struct {
	RailUUID      string                  `json:"rail_uuid"`
	Status        string                  `json:"status"`
	CompletedCard *ebs_fields.CardSummary `json:"completed_card,omitempty"`
}

type ClaimCardEnrollmentRailCommand struct {
	EnrollmentID string `json:"enrollment_id"`
}

type ClaimCardEnrollmentRailResult struct {
	Granted bool `json:"granted"`
}

type CompleteCardEnrollmentCommand struct {
	EnrollmentID string `json:"enrollment_id"`
	PAN          string `json:"pan"`
	Expiry       string `json:"exp_date"`
	Name         string `json:"name"`
}

type FailCardEnrollmentCommand struct {
	EnrollmentID string `json:"enrollment_id"`
	FailureCode  string `json:"failure_code"`
}

func (s *Service) ListOpaqueCardsForUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.CardSummary, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	return s.Store.ListActiveCardSummaries(ctx, tenantID, userID)
}

func (s *Service) RenameOpaqueCardForUserID(ctx context.Context, tenantID string, userID int64, cardID, name string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.UpdateActiveCardName(ctx, tenantID, userID, cardID, name)
}

func (s *Service) RetireOpaqueCardForUserID(ctx context.Context, tenantID string, userID int64, cardID string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.RetireActiveCard(ctx, tenantID, userID, cardID)
}

func (s *Service) SetOpaqueMainCardForUserID(ctx context.Context, tenantID string, userID int64, cardID string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.SetActiveMainCard(ctx, tenantID, userID, cardID)
}

func (s *Service) CreateCardEnrollmentIntentForUserID(ctx context.Context, tenantID string, userID int64, now time.Time) (CardEnrollmentIntentCommandResult, error) {
	if s == nil || s.Store == nil {
		return CardEnrollmentIntentCommandResult{}, ErrMissingStore
	}
	intent, err := s.Store.CreateCardEnrollmentIntent(ctx, tenantID, userID, now, cardEnrollmentIntentTTL)
	if err != nil {
		return CardEnrollmentIntentCommandResult{}, err
	}
	return CardEnrollmentIntentCommandResult{
		EnrollmentID: intent.EnrollmentID,
		RailUUID:     intent.RailUUID,
		ExpiresAt:    intent.ExpiresAt,
	}, nil
}

func (s *Service) BeginCardEnrollmentForUserID(ctx context.Context, tenantID string, userID int64, cmd BeginCardEnrollmentCommand, now time.Time) (BeginCardEnrollmentResult, error) {
	if s == nil || s.Store == nil {
		return BeginCardEnrollmentResult{}, ErrMissingStore
	}
	intent, err := s.Store.BeginCardEnrollmentIntent(ctx, tenantID, userID, cmd.EnrollmentID, store.CardEnrollmentAttempt{
		PAN:           cmd.PAN,
		Expiry:        cmd.Expiry,
		Name:          cmd.Name,
		OperationKind: store.CardEnrollmentOperation,
	}, now)
	if err != nil {
		return BeginCardEnrollmentResult{}, err
	}
	return BeginCardEnrollmentResult{
		RailUUID:      intent.RailUUID,
		Status:        intent.Status,
		CompletedCard: intent.CompletedCard,
	}, nil
}

func (s *Service) ClaimCardEnrollmentRailForUserID(ctx context.Context, tenantID string, userID int64, cmd ClaimCardEnrollmentRailCommand, now time.Time) (ClaimCardEnrollmentRailResult, error) {
	if s == nil || s.Store == nil {
		return ClaimCardEnrollmentRailResult{}, ErrMissingStore
	}
	granted, err := s.Store.ClaimCardEnrollmentRailSubmission(ctx, tenantID, userID, cmd.EnrollmentID, now)
	if err != nil {
		return ClaimCardEnrollmentRailResult{}, err
	}
	return ClaimCardEnrollmentRailResult{Granted: granted}, nil
}

func (s *Service) CompleteCardEnrollmentForUserID(ctx context.Context, tenantID string, userID int64, cmd CompleteCardEnrollmentCommand, now time.Time) (ebs_fields.CardSummary, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.CardSummary{}, ErrMissingStore
	}
	return s.Store.CompleteCardEnrollmentIntent(ctx, tenantID, userID, cmd.EnrollmentID, store.VerifiedCardEnrollment{
		PAN:                cmd.PAN,
		Expiry:             cmd.Expiry,
		Name:               cmd.Name,
		VerificationMethod: cardEnrollmentVerification,
	}, now)
}

func (s *Service) FailCardEnrollmentForUserID(ctx context.Context, tenantID string, userID int64, cmd FailCardEnrollmentCommand, now time.Time) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.FailCardEnrollmentIntent(ctx, tenantID, userID, cmd.EnrollmentID, cmd.FailureCode, now)
}

func (s *Service) CreateOpaqueCardEnrollmentIntent(ctx context.Context, tenantID string, userID int64) (CardEnrollmentIntentResponse, error) {
	if s == nil {
		return CardEnrollmentIntentResponse{}, ErrMissingService
	}
	_, key, keyID, err := parseEnrollmentRailKey(s.NoebsConfig.EBSConsumerKey)
	if err != nil {
		return CardEnrollmentIntentResponse{}, err
	}
	intent, err := s.createCardEnrollmentIntentInCardVault(ctx, tenantID, userID)
	if err != nil {
		return CardEnrollmentIntentResponse{}, err
	}
	return CardEnrollmentIntentResponse{
		EnrollmentID: intent.EnrollmentID,
		RailUUID:     intent.RailUUID,
		ExpiresAt:    intent.ExpiresAt,
		RailKey: CardEnrollmentKeyMetadata{
			Algorithm: cardEnrollmentPublicAlgorithm,
			KeyID:     keyID,
			PublicKey: key,
		},
	}, nil
}

func (s *Service) ConfirmOpaqueCardEnrollment(ctx context.Context, tenantID string, userID int64, enrollmentID string, req ConfirmCardEnrollmentRequest) (ebs_fields.CardSummary, error) {
	if s == nil {
		return ebs_fields.CardSummary{}, ErrMissingService
	}
	publicKey, _, _, err := parseEnrollmentRailKey(s.NoebsConfig.EBSConsumerKey)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	enrollmentID, err = store.NormalizeEnrollmentID(enrollmentID)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	req.RailUUID, err = store.NormalizeRailUUID(req.RailUUID)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	req.PAN = strings.TrimSpace(req.PAN)
	req.Expiry = strings.TrimSpace(req.Expiry)
	req.Name = strings.TrimSpace(req.Name)
	if err := validateEnrollmentIPINBlock(req.IPINBlock, publicKey.Size()); err != nil {
		return ebs_fields.CardSummary{}, err
	}
	if s.HTTPClient == nil {
		return ebs_fields.CardSummary{}, ErrMissingHTTPClient
	}

	begin, err := s.beginCardEnrollmentInCardVault(ctx, tenantID, userID, BeginCardEnrollmentCommand{
		EnrollmentID: enrollmentID,
		PAN:          req.PAN,
		Expiry:       req.Expiry,
		Name:         req.Name,
	})
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	if begin.RailUUID != req.RailUUID {
		return ebs_fields.CardSummary{}, ErrEnrollmentRailUUIDMismatch
	}
	if begin.CompletedCard != nil {
		return *begin.CompletedCard, nil
	}

	claim, err := s.claimCardEnrollmentRailInCardVault(ctx, tenantID, userID, enrollmentID)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	if claim.Granted {
		if err := s.verifyCardEnrollmentWithEBS(ctx, tenantID, req); err != nil {
			if errors.Is(err, ErrInvalidCard) {
				_ = s.failCardEnrollmentInCardVault(ctx, tenantID, userID, enrollmentID, "rail_verification_rejected")
			}
			return ebs_fields.CardSummary{}, err
		}
	} else if err := s.reconcileCardEnrollmentWithEBS(ctx, tenantID, req.RailUUID); err != nil {
		return ebs_fields.CardSummary{}, err
	}

	return s.completeCardEnrollmentInCardVault(ctx, tenantID, userID, CompleteCardEnrollmentCommand{
		EnrollmentID: enrollmentID,
		PAN:          req.PAN,
		Expiry:       req.Expiry,
		Name:         req.Name,
	})
}

func parseEnrollmentRailKey(value string) (*rsa.PublicKey, string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, "", "", ErrMissingEnrollmentPublicKey
	}
	der, err := base64.StdEncoding.DecodeString(value)
	if err != nil || base64.StdEncoding.EncodeToString(der) != value {
		return nil, "", "", ErrInvalidEnrollmentPublicKey
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, "", "", ErrInvalidEnrollmentPublicKey
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok || publicKey.N.BitLen() < 2048 || publicKey.N.BitLen() > 4096 {
		return nil, "", "", ErrInvalidEnrollmentPublicKey
	}
	digest := sha256.Sum256(der)
	return publicKey, value, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateEnrollmentIPINBlock(value string, ciphertextBytes int) error {
	if value == "" {
		return ErrMissingIPINBlock
	}
	if value != strings.TrimSpace(value) {
		return ErrInvalidIPINBlock
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != ciphertextBytes || base64.StdEncoding.EncodeToString(decoded) != value {
		return ErrInvalidIPINBlock
	}
	return nil
}

func (s *Service) verifyCardEnrollmentWithEBS(ctx context.Context, tenantID string, req ConfirmCardEnrollmentRequest) error {
	if _, err := store.ValidateTenantID(tenantID); err != nil {
		return err
	}
	fields := ebs_fields.ConsumerBalanceFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: s.NoebsConfig.ConsumerID,
			TranDateTime:  ebs_fields.EbsDate(),
			UUID:          req.RailUUID,
		},
		ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
			Pan: req.PAN, Ipin: req.IPINBlock, ExpDate: req.Expiry,
		},
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, response, err := ebs_fields.EBSHttpClientWithClient(
		s.HTTPClient,
		s.NoebsConfig.ConsumerIP+ebs_fields.ConsumerBalanceEndpoint,
		payload,
	)
	if err != nil {
		if response.ResponseCode != 0 {
			return ErrInvalidCard
		}
		return ErrEnrollmentOutcomeUnknown
	}
	if response.ResponseCode != ebs_fields.SUCCESS || response.UUID != req.RailUUID {
		return ErrInvalidCard
	}
	return nil
}

func (s *Service) reconcileCardEnrollmentWithEBS(ctx context.Context, tenantID, railUUID string) error {
	if _, err := store.ValidateTenantID(tenantID); err != nil {
		return err
	}
	statusUUID, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	fields := ebs_fields.ConsumerTransactionStatusFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: s.NoebsConfig.ConsumerID,
			TranDateTime:  ebs_fields.EbsDate(),
			UUID:          statusUUID.String(),
		},
		OriginalTranUUID: railUUID,
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, response, err := ebs_fields.EBSHttpClientWithClient(
		s.HTTPClient,
		s.NoebsConfig.ConsumerIP+ebs_fields.ConsumerTransactionStatusEndpoint,
		payload,
	)
	if err != nil || response.ResponseCode != ebs_fields.SUCCESS ||
		response.OriginalTransaction.ResponseCode != ebs_fields.SUCCESS ||
		response.OriginalTransaction.UUID != railUUID {
		return ErrEnrollmentOutcomeUnknown
	}
	return nil
}

func (s *Service) createCardEnrollmentIntentInCardVault(ctx context.Context, tenantID string, userID int64) (CardEnrollmentIntentCommandResult, error) {
	var result CardEnrollmentIntentCommandResult
	if err := s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/enrollment-intents", CreateCardEnrollmentIntentCommand{}, &result); err != nil {
		return CardEnrollmentIntentCommandResult{}, err
	}
	return result, nil
}

func (s *Service) beginCardEnrollmentInCardVault(ctx context.Context, tenantID string, userID int64, cmd BeginCardEnrollmentCommand) (BeginCardEnrollmentResult, error) {
	var result BeginCardEnrollmentResult
	if err := s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/enrollment-intents/begin", cmd, &result); err != nil {
		return BeginCardEnrollmentResult{}, err
	}
	return result, nil
}

func (s *Service) claimCardEnrollmentRailInCardVault(ctx context.Context, tenantID string, userID int64, enrollmentID string) (ClaimCardEnrollmentRailResult, error) {
	var result ClaimCardEnrollmentRailResult
	err := s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/enrollment-intents/claim-rail", ClaimCardEnrollmentRailCommand{EnrollmentID: enrollmentID}, &result)
	return result, err
}

func (s *Service) completeCardEnrollmentInCardVault(ctx context.Context, tenantID string, userID int64, cmd CompleteCardEnrollmentCommand) (ebs_fields.CardSummary, error) {
	var result ebs_fields.CardSummary
	if err := s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/enrollment-intents/complete", cmd, &result); err != nil {
		return ebs_fields.CardSummary{}, err
	}
	return result, nil
}

func (s *Service) failCardEnrollmentInCardVault(ctx context.Context, tenantID string, userID int64, enrollmentID, failureCode string) error {
	return s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/enrollment-intents/fail", FailCardEnrollmentCommand{
		EnrollmentID: enrollmentID,
		FailureCode:  failureCode,
	}, nil)
}
