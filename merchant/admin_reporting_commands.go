package merchant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

const adminReportingServiceDiscoveryKey = "admin-reporting"

type transactionProjectionCommand struct {
	Transaction *ebs_fields.EBSResponse `json:"transaction"`
}

type adminReportingErrorPayload struct {
	Code string `json:"code"`
}

func (s *Service) requireTransactionProjectionTarget() error {
	if s == nil {
		return ErrMissingService
	}
	if s.Store == nil {
		return ErrMissingStore
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	if _, err := s.adminReportingEndpoint(); err != nil {
		return err
	}
	return nil
}

func (s *Service) StoreTransactionProjectionInAdminReporting(ctx context.Context, tenantID string, transaction ebs_fields.EBSResponse) error {
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	return s.doAdminReportingCommand(ctx, tenantID, "/internal/admin-reporting/transactions", transactionProjectionCommand{Transaction: &transaction})
}

func (s *Service) doAdminReportingCommand(ctx context.Context, tenantID, path string, command any) error {
	if s == nil {
		return ErrMissingService
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	endpoint, err := s.adminReportingEndpoint()
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
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set(gateway.GatewayAdminIdentityHeader, gateway.GatewayAdminIdentityValue)
	req.Header.Set(gateway.GatewayAdminRoleHeader, gateway.GatewayAdminRoleValue)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAdminReportingCommand, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return adminReportingCommandError(resp.StatusCode, data)
	}
	return nil
}

func (s *Service) adminReportingEndpoint() (string, error) {
	if s == nil {
		return "", ErrMissingService
	}
	endpoint := strings.TrimSpace(s.NoebsConfig.ServiceDiscovery[adminReportingServiceDiscoveryKey])
	if endpoint == "" {
		return "", ErrMissingAdminReporting
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidAdminReporting, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: scheme %q", ErrInvalidAdminReporting, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidAdminReporting)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func adminReportingCommandError(status int, data []byte) error {
	var payload adminReportingErrorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("%w: status %d", ErrAdminReportingCommand, status)
	}
	return fmt.Errorf("%w: status %d code %q", ErrAdminReportingCommand, status, payload.Code)
}
