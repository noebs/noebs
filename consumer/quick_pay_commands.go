package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

const (
	cardVaultServiceDiscoveryKey    = "card-vault"
	identityAuthServiceDiscoveryKey = "identity-auth"
	notificationServiceDiscoveryKey = "notification-chat"
)

type serviceCommandTarget struct {
	discoveryKey string
	missingErr   error
	invalidErr   error
	commandErr   error
}

var (
	cardVaultCommandTarget = serviceCommandTarget{
		discoveryKey: cardVaultServiceDiscoveryKey,
		missingErr:   ErrMissingCardVault,
		invalidErr:   ErrInvalidCardVault,
		commandErr:   ErrCardVaultCommand,
	}
	identityAuthCommandTarget = serviceCommandTarget{
		discoveryKey: identityAuthServiceDiscoveryKey,
		missingErr:   ErrMissingIdentityAuth,
		invalidErr:   ErrInvalidIdentityAuth,
		commandErr:   ErrIdentityAuthCommand,
	}
	notificationCommandTarget = serviceCommandTarget{
		discoveryKey: notificationServiceDiscoveryKey,
		missingErr:   ErrMissingNotification,
		invalidErr:   ErrInvalidNotification,
		commandErr:   ErrNotificationCommand,
	}
)

type QuickPaymentTokenResolveCommand struct {
	BodyToken  string `json:"body_token,omitempty"`
	QueryToken string `json:"query_token,omitempty"`
	UUID       string `json:"uuid,omitempty"`
	Amount     int    `json:"amount,omitempty"`
}

type QuickPaymentTokenResolution struct {
	UUID            string `json:"uuid"`
	RailUUID        string `json:"rail_uuid"`
	ToCard          string `json:"to_card"`
	Amount          int    `json:"amount"`
	RecipientUserID int64  `json:"recipient_user_id"`
}

type QuickPaymentTokenFinalizationCommand struct {
	UUID     string `json:"uuid"`
	RailUUID string `json:"rail_uuid"`
	Status   string `json:"status"`
}

type serviceCommandErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Service) ResolveQuickPaymentTokenForUserID(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenResolveCommand) (QuickPaymentTokenResolution, error) {
	if s == nil || s.Store == nil {
		return QuickPaymentTokenResolution{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	if userID <= 0 {
		return QuickPaymentTokenResolution{}, store.ErrInvalidUserID
	}

	tokenUUID, err := quickPaymentTokenUUID(ebs_fields.QuickPaymentFields{EncodedPaymentToken: cmd.BodyToken}, cmd.UUID, cmd.QueryToken)
	if err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	storedToken, err := s.Store.GetTokenByUUID(ctx, tenantID, tokenUUID)
	if err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	amount, err := resolveQuickPaymentAmount(storedToken.Amount, cmd.Amount)
	if err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	if err := s.Store.ClaimTokenForPayment(ctx, tenantID, tokenUUID, userID, amount); err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	claimedToken, err := s.Store.GetTokenByUUID(ctx, tenantID, tokenUUID)
	if err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	if claimedToken.UserID <= 0 {
		return QuickPaymentTokenResolution{}, store.ErrInvalidUserID
	}
	return QuickPaymentTokenResolution{
		UUID:            claimedToken.UUID,
		RailUUID:        claimedToken.RailUUID,
		ToCard:          claimedToken.ToCard,
		Amount:          amount,
		RecipientUserID: claimedToken.UserID,
	}, nil
}

func resolveQuickPaymentAmount(storedAmount, requestedAmount int) (int, error) {
	if storedAmount < 0 {
		return 0, store.ErrInvalidAmount
	}
	if storedAmount == 0 {
		if requestedAmount <= 0 {
			return 0, store.ErrInvalidAmount
		}
		return requestedAmount, nil
	}
	if requestedAmount != storedAmount {
		return 0, ErrAmountMismatch
	}
	return storedAmount, nil
}

func (s *Service) FinalizeQuickPaymentTokenForUserID(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenFinalizationCommand) error {
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
	tokenUUID := strings.TrimSpace(cmd.UUID)
	if tokenUUID == "" {
		return ErrMissingUUID
	}
	railUUID := strings.TrimSpace(cmd.RailUUID)
	if railUUID == "" {
		return ErrMissingUUID
	}
	return s.Store.FinalizeTokenPayment(ctx, tenantID, tokenUUID, railUUID, userID, strings.TrimSpace(cmd.Status))
}

func (s *Service) ResolveQuickPaymentTokenFromCardVault(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenResolveCommand) (QuickPaymentTokenResolution, error) {
	var resolution QuickPaymentTokenResolution
	if err := s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/quick-pay/resolve", cmd, &resolution); err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	return resolution, nil
}

func (s *Service) FinalizeQuickPaymentTokenInCardVault(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenFinalizationCommand) error {
	return s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/quick-pay/finalize", cmd, nil)
}

func (s *Service) doCardVaultCommand(ctx context.Context, tenantID string, userID int64, path string, command any, out any) error {
	if s == nil {
		return ErrMissingService
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	endpoint, err := s.serviceDiscoveryEndpoint(cardVaultCommandTarget)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	req.Header.Set(gateway.GatewayUserIDHeader, strconv.FormatInt(userID, 10))

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCardVaultCommand, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return serviceCommandError(cardVaultCommandTarget.commandErr, resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

func (s *Service) doAdminServiceCommand(ctx context.Context, tenantID string, target serviceCommandTarget, path string, command any, out any) error {
	if s == nil {
		return ErrMissingService
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	endpoint, err := s.serviceDiscoveryEndpoint(target)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	req.Header.Set(gateway.GatewayAdminIdentityHeader, gateway.GatewayAdminIdentityValue)
	req.Header.Set(gateway.GatewayAdminRoleHeader, gateway.GatewayAdminRoleValue)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", target.commandErr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return serviceCommandError(target.commandErr, resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

func (s *Service) serviceDiscoveryEndpoint(target serviceCommandTarget) (string, error) {
	if s == nil {
		return "", ErrMissingService
	}
	endpoint := strings.TrimSpace(s.NoebsConfig.ServiceDiscovery[target.discoveryKey])
	if endpoint == "" {
		return "", target.missingErr
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("%w: %v", target.invalidErr, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: scheme %q", target.invalidErr, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w: missing host", target.invalidErr)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func serviceCommandError(commandErr error, status int, data []byte) error {
	var payload serviceCommandErrorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("%w: status %d", commandErr, status)
	}
	if err := errorForServiceCommandCode(payload.Code); err != nil {
		return err
	}
	return fmt.Errorf("%w: status %d code %q", commandErr, status, payload.Code)
}

func errorForServiceCommandCode(code string) error {
	switch strings.TrimSpace(code) {
	case "":
		return nil
	case ErrMissingUUID.Error():
		return ErrMissingUUID
	case ErrAmountMismatch.Error():
		return ErrAmountMismatch
	case store.ErrPaymentTokenUnavailable.Error():
		return store.ErrPaymentTokenUnavailable
	case store.ErrInvalidPaymentTokenStatus.Error():
		return store.ErrInvalidPaymentTokenStatus
	case ErrCardNotMatched.Error():
		return ErrCardNotMatched
	case ErrTransactionFailed.Error():
		return ErrTransactionFailed
	case ErrInvalidPaymentToken.Error():
		return ErrInvalidPaymentToken
	case ErrAmbiguousPaymentToken.Error():
		return ErrAmbiguousPaymentToken
	case ErrInvalidQuickPaymentRequest.Error():
		return ErrInvalidQuickPaymentRequest
	case ErrPaymentOutcomeUnknown.Error():
		return ErrPaymentOutcomeUnknown
	case ErrInvalidPaymentInfo.Error():
		return ErrInvalidPaymentInfo
	case ErrMissingBillerID.Error():
		return ErrMissingBillerID
	case ErrMissingStore.Error():
		return ErrMissingStore
	case ErrNotificationCommand.Error():
		return ErrNotificationCommand
	case ErrMissingMobile.Error():
		return ErrMissingMobile
	case ErrMissingPassword.Error():
		return ErrMissingPassword
	case ErrMissingIssuedPAN.Error():
		return ErrMissingIssuedPAN
	case ErrMissingCardExpiry.Error():
		return ErrMissingCardExpiry
	case ErrInvalidRecoveryCredential.Error():
		return ErrInvalidRecoveryCredential
	case ErrInvalidBillerHookEndpoint.Error():
		return ErrInvalidBillerHookEndpoint
	case ErrBillerHookPost.Error():
		return ErrBillerHookPost
	case ErrUserAlreadyExists.Error():
		return ErrUserAlreadyExists
	case ErrInvalidCard.Error():
		return ErrInvalidCard
	case ErrReceiverHasNoCard.Error():
		return ErrReceiverHasNoCard
	case store.ErrMissingTenantID.Error():
		return store.ErrMissingTenantID
	case store.ErrInvalidTenantID.Error():
		return store.ErrInvalidTenantID
	case store.ErrInvalidUserID.Error():
		return store.ErrInvalidUserID
	case store.ErrInvalidAmount.Error():
		return store.ErrInvalidAmount
	case store.ErrMissingPAN.Error():
		return store.ErrMissingPAN
	case store.ErrMissingCardID.Error():
		return store.ErrMissingCardID
	case store.ErrInvalidCardID.Error():
		return store.ErrInvalidCardID
	case store.ErrCardNotFound.Error():
		return store.ErrCardNotFound
	case store.ErrCardEnrollmentConflict.Error():
		return store.ErrCardEnrollmentConflict
	case store.ErrEnrollmentIntentOpen.Error():
		return store.ErrEnrollmentIntentOpen
	case store.ErrEnrollmentIntentNotFound.Error():
		return store.ErrEnrollmentIntentNotFound
	case store.ErrEnrollmentIntentExpired.Error():
		return store.ErrEnrollmentIntentExpired
	case store.ErrEnrollmentIntentConsumed.Error():
		return store.ErrEnrollmentIntentConsumed
	case store.ErrEnrollmentClaimMismatch.Error():
		return store.ErrEnrollmentClaimMismatch
	case store.ErrInvalidEnrollmentIntent.Error():
		return store.ErrInvalidEnrollmentIntent
	case store.ErrInvalidCardExpiry.Error():
		return store.ErrInvalidCardExpiry
	case store.ErrMissingRailUUID.Error():
		return store.ErrMissingRailUUID
	case store.ErrInvalidRailUUID.Error():
		return store.ErrInvalidRailUUID
	case store.ErrInvalidFundedPurpose.Error():
		return store.ErrInvalidFundedPurpose
	case store.ErrInvalidFundedBodyClaim.Error():
		return store.ErrInvalidFundedBodyClaim
	case store.ErrFundedClaimMismatch.Error():
		return store.ErrFundedClaimMismatch
	case store.ErrInvalidRailTranDateTime.Error():
		return store.ErrInvalidRailTranDateTime
	default:
		return nil
	}
}
