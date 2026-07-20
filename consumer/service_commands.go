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
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

const (
	cardVaultServiceDiscoveryKey    = "card-vault"
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
	notificationCommandTarget = serviceCommandTarget{
		discoveryKey: notificationServiceDiscoveryKey,
		missingErr:   ErrMissingNotification,
		invalidErr:   ErrInvalidNotification,
		commandErr:   ErrNotificationCommand,
	}
)

type serviceCommandErrorPayload struct {
	Code string `json:"code"`
}

func (s *Service) doCardVaultCommand(ctx context.Context, tenantID string, userID int64, path string, command any, out any) error {
	if s == nil {
		return ErrMissingService
	}
	client := s.internalHTTPClient()
	if client == nil {
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
	req.Header.Set(workloadauth.HeaderRequestID, uuid.NewString())
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	req.Header.Set(gateway.GatewayUserIDHeader, strconv.FormatInt(userID, 10))
	if err := s.signServiceCommand(cardVaultServiceDiscoveryKey, req, payload); err != nil {
		return err
	}
	return executeServiceCommand(client, req, cardVaultCommandTarget.commandErr, out)
}

func (s *Service) doAdminServiceCommand(ctx context.Context, tenantID string, target serviceCommandTarget, path string, command any, out any) error {
	if s == nil {
		return ErrMissingService
	}
	client := s.internalHTTPClient()
	if client == nil {
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
	req.Header.Set(workloadauth.HeaderRequestID, uuid.NewString())
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	if err := s.signServiceCommand(target.discoveryKey, req, payload); err != nil {
		return err
	}
	return executeServiceCommand(client, req, target.commandErr, out)
}

func executeServiceCommand(client *http.Client, req *http.Request, commandErr error, out any) error {
	resp, err := doInternalRequest(client, req)
	if err != nil {
		return fmt.Errorf("%w: %v", commandErr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return serviceCommandError(commandErr, resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (s *Service) signServiceCommand(audience string, req *http.Request, payload []byte) error {
	if s.WorkloadSigners == nil {
		return workloadauth.ErrMissingSigner
	}
	return s.WorkloadSigners.Sign(audience, req, payload)
}

func doInternalRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	copyClient := *client
	copyClient.CheckRedirect = workloadauth.RejectRedirect
	return copyClient.Do(req)
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
	case ErrMissingIssuedPAN.Error():
		return ErrMissingIssuedPAN
	case ErrMissingCardExpiry.Error():
		return ErrMissingCardExpiry
	case ErrInvalidCard.Error():
		return ErrInvalidCard
	case store.ErrMissingTenantID.Error():
		return store.ErrMissingTenantID
	case store.ErrInvalidTenantID.Error():
		return store.ErrInvalidTenantID
	case store.ErrInvalidUserID.Error():
		return store.ErrInvalidUserID
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
