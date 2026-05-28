package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

type BillerHookCommand struct {
	EBS          ebs_fields.EBSResponse `json:"ebs_response"`
	IsSuccessful bool                   `json:"is_successful"`
	Token        string                 `json:"payment_token"`
}

type billerHookPayload struct {
	PaymentToken    string  `json:"payment_token"`
	IsSuccessful    bool    `json:"is_successful"`
	ResponseCode    int     `json:"response_code"`
	ResponseMessage string  `json:"response_message,omitempty"`
	ResponseStatus  string  `json:"response_status,omitempty"`
	UUID            string  `json:"uuid,omitempty"`
	TranAmount      float32 `json:"tran_amount,omitempty"`
	ApprovalCode    string  `json:"approval_code,omitempty"`
	ReferenceNumber string  `json:"reference_number,omitempty"`
	TranDateTime    string  `json:"tran_date_time,omitempty"`
}

func (s *Service) SubmitBillerHook(ctx context.Context, tenantID string, cmd BillerHookCommand) error {
	if s == nil {
		return ErrMissingService
	}
	_, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cmd.Token)
	if token == "" {
		return ErrMissingUUID
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	hookURL := strings.TrimSpace(s.NoebsConfig.ConsumerBillerHooksURL)
	if hookURL == "" {
		return nil
	}
	if err := validateBillerHookURL(hookURL, s.NoebsConfig.IsDebug); err != nil {
		return err
	}

	payload := billerHookPayload{
		PaymentToken:    token,
		IsSuccessful:    cmd.IsSuccessful,
		ResponseCode:    cmd.EBS.ResponseCode,
		ResponseMessage: cmd.EBS.ResponseMessage,
		ResponseStatus:  cmd.EBS.ResponseStatus,
		UUID:            cmd.EBS.UUID,
		TranAmount:      cmd.EBS.TranAmount,
		ApprovalCode:    cmd.EBS.ApprovalCode,
		ReferenceNumber: cmd.EBS.ReferenceNumber,
		TranDateTime:    cmd.EBS.TranDateTime,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hookURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBillerHookPost, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: status %d", ErrBillerHookPost, resp.StatusCode)
	}
	return nil
}

func (s *Service) SubmitBillerHookInNotificationChat(ctx context.Context, tenantID string, cmd BillerHookCommand) error {
	return s.doAdminServiceCommand(ctx, tenantID, notificationCommandTarget, "/internal/notification-chat/biller-hook", cmd, nil)
}

func validateBillerHookURL(rawURL string, debug bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBillerHookEndpoint, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%w: missing scheme or host", ErrInvalidBillerHookEndpoint)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && debug {
		return nil
	}
	return fmt.Errorf("%w: scheme %q", ErrInvalidBillerHookEndpoint, parsed.Scheme)
}
