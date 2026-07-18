package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/google/uuid"
	chat "github.com/tutipay/ws"
)

type chatSessionValidator struct {
	endpoint string
	client   *http.Client
	signers  *workloadauth.SignerSet
}

func newChatSessionValidator(cfg ebs_fields.NoebsConfig, signers *workloadauth.SignerSet) (*chatSessionValidator, error) {
	if signers == nil {
		return nil, workloadauth.ErrMissingSigner
	}
	endpoint, err := serviceDiscoveryEndpoint(cfg, serviceRoleIdentityAuth)
	if err != nil {
		return nil, err
	}
	return &chatSessionValidator{
		endpoint: endpoint,
		client: httpclient.New(
			httpclient.WithTimeout(2*time.Second),
			httpclient.WithResponseHeaderTimeout(1500*time.Millisecond),
		),
		signers: signers,
	}, nil
}

func (v *chatSessionValidator) ValidateSession(ctx context.Context, identity chatGatewayIdentity) error {
	if v == nil || v.client == nil || v.endpoint == "" || strings.TrimSpace(identity.Token) == "" {
		return gateway.ErrSessionValidation
	}
	if v.signers == nil {
		return fmt.Errorf("%w: %w", gateway.ErrSessionValidation, workloadauth.ErrMissingSigner)
	}
	payload, err := json.Marshal(consumer.SessionValidationCommand{
		UserID:       identity.UserID,
		SessionEpoch: identity.SessionEpoch,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", gateway.ErrSessionValidation, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint+"/internal/identity-auth/sessions/validate", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: %v", gateway.ErrSessionValidation, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workloadauth.HeaderRequestID, uuid.NewString())
	req.Header.Set(gateway.GatewayTenantIDHeader, identity.TenantID)
	if err := v.signers.Sign(string(serviceRoleIdentityAuth), req, payload); err != nil {
		return fmt.Errorf("%w: %v", gateway.ErrSessionValidation, err)
	}

	client := *v.client
	client.CheckRedirect = workloadauth.RejectRedirect
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", gateway.ErrSessionValidation, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusUnauthorized:
		return gateway.ErrSessionRevoked
	default:
		return fmt.Errorf("%w: identity-auth returned %s", gateway.ErrSessionValidation, resp.Status)
	}
}

func chatSessionValidation(validator interface {
	ValidateSession(context.Context, chatGatewayIdentity) error
}) func(context.Context) error {
	return func(ctx context.Context) error {
		if validator == nil {
			return chat.ErrSessionValidationUnavailable
		}
		identity, ok := ctx.Value(chatGatewayIdentityContextKey{}).(chatGatewayIdentity)
		if !ok || identity.SessionEpoch <= 0 || strings.TrimSpace(identity.Token) == "" {
			return chat.ErrUnauthorized
		}
		err := validator.ValidateSession(ctx, identity)
		if errors.Is(err, gateway.ErrSessionValidation) {
			return chat.ErrSessionValidationUnavailable
		}
		return err
	}
}
