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
	internalTenantIDHeader          = "X-Tenant-ID"
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
)

type QuickPaymentTokenResolveCommand struct {
	BodyToken  string `json:"body_token,omitempty"`
	QueryToken string `json:"query_token,omitempty"`
	UUID       string `json:"uuid,omitempty"`
	Amount     int    `json:"amount,omitempty"`
}

type QuickPaymentTokenResolution struct {
	UUID   string `json:"uuid"`
	ToCard string `json:"to_card"`
	Amount int    `json:"amount"`
}

type QuickPaymentTokenPaidCommand struct {
	UUID string `json:"uuid"`
}

type serviceCommandErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Service) ResolveQuickPaymentTokenForUserID(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenResolveCommand) (QuickPaymentTokenResolution, error) {
	if s == nil || s.Store == nil {
		return QuickPaymentTokenResolution{}, ErrMissingStore
	}
	if tenantID == "" {
		return QuickPaymentTokenResolution{}, store.ErrMissingTenantID
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
	if storedToken.UserID != userID {
		return QuickPaymentTokenResolution{}, store.ErrInvalidUserID
	}
	if storedToken.Amount != 0 && cmd.Amount != storedToken.Amount {
		return QuickPaymentTokenResolution{}, ErrAmountMismatch
	}
	return QuickPaymentTokenResolution{
		UUID:   storedToken.UUID,
		ToCard: storedToken.ToCard,
		Amount: storedToken.Amount,
	}, nil
}

func (s *Service) MarkQuickPaymentTokenPaidForUserID(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenPaidCommand) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	tokenUUID := strings.TrimSpace(cmd.UUID)
	if tokenUUID == "" {
		return ErrMissingUUID
	}
	storedToken, err := s.Store.GetTokenByUUID(ctx, tenantID, tokenUUID)
	if err != nil {
		return err
	}
	if storedToken.UserID != userID {
		return store.ErrInvalidUserID
	}
	return s.Store.MarkTokenPaid(ctx, tenantID, tokenUUID)
}

func (s *Service) ResolveQuickPaymentTokenFromCardVault(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenResolveCommand) (QuickPaymentTokenResolution, error) {
	var resolution QuickPaymentTokenResolution
	if err := s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/quick-pay/resolve", cmd, &resolution); err != nil {
		return QuickPaymentTokenResolution{}, err
	}
	return resolution, nil
}

func (s *Service) MarkQuickPaymentTokenPaidInCardVault(ctx context.Context, tenantID string, userID int64, cmd QuickPaymentTokenPaidCommand) error {
	return s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/quick-pay/mark-paid", cmd, nil)
}

func (s *Service) doCardVaultCommand(ctx context.Context, tenantID string, userID int64, path string, command any, out any) error {
	if s == nil {
		return ErrMissingService
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
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
	if tenantID == "" {
		return store.ErrMissingTenantID
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
	req.Header.Set(internalTenantIDHeader, tenantID)
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
	case ErrInvalidPaymentToken.Error():
		return ErrInvalidPaymentToken
	case ErrAmbiguousPaymentToken.Error():
		return ErrAmbiguousPaymentToken
	case ErrMissingStore.Error():
		return ErrMissingStore
	case ErrMissingMobile.Error():
		return ErrMissingMobile
	case ErrMissingPassword.Error():
		return ErrMissingPassword
	case ErrMissingIssuedPAN.Error():
		return ErrMissingIssuedPAN
	case ErrReceiverHasNoCard.Error():
		return ErrReceiverHasNoCard
	case store.ErrMissingTenantID.Error():
		return store.ErrMissingTenantID
	case store.ErrInvalidTenantID.Error():
		return store.ErrInvalidTenantID
	case store.ErrInvalidUserID.Error():
		return store.ErrInvalidUserID
	default:
		return nil
	}
}
