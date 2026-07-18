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
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/gofiber/fiber/v2"
	chat "github.com/tutipay/ws"
)

const gatewaySessionValidationPath = "/internal/api-gateway/sessions/validate"

type gatewaySessionValidationCommand struct {
	TenantID     string `json:"tenant_id"`
	UserID       int64  `json:"user_id"`
	Mobile       string `json:"mobile"`
	SessionEpoch int64  `json:"session_epoch"`
}

type gatewaySessionValidator struct {
	endpoint string
	client   *http.Client
}

func newGatewaySessionValidator(cfg ebs_fields.NoebsConfig) (*gatewaySessionValidator, error) {
	endpoint, err := serviceDiscoveryEndpoint(cfg, serviceRoleAPIGateway)
	if err != nil {
		return nil, err
	}
	return &gatewaySessionValidator{
		endpoint: endpoint,
		client: httpclient.New(
			httpclient.WithTimeout(2*time.Second),
			httpclient.WithResponseHeaderTimeout(1500*time.Millisecond),
		),
	}, nil
}

func (v *gatewaySessionValidator) ValidateSession(ctx context.Context, identity chatGatewayIdentity) error {
	if v == nil || v.client == nil || v.endpoint == "" || strings.TrimSpace(identity.Token) == "" {
		return gateway.ErrSessionValidation
	}
	payload, err := json.Marshal(gatewaySessionValidationCommand{
		TenantID:     identity.TenantID,
		UserID:       identity.UserID,
		Mobile:       identity.Mobile,
		SessionEpoch: identity.SessionEpoch,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", gateway.ErrSessionValidation, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint+gatewaySessionValidationPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: %v", gateway.ErrSessionValidation, err)
	}
	req.Header.Set("Authorization", "Bearer "+identity.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.client.Do(req)
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
		return fmt.Errorf("%w: api-gateway returned %s", gateway.ErrSessionValidation, resp.Status)
	}
}

func registerGatewaySessionValidationRoute(route *fiber.App, jwt gateway.JWTAuth) {
	route.Post(gatewaySessionValidationPath, jwt.AuthMiddleware(), validateGatewaySessionIdentity)
}

func validateGatewaySessionIdentity(c *fiber.Ctx) error {
	var command gatewaySessionValidationCommand
	if err := json.Unmarshal(c.Body(), &command); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": "Malformed request"})
	}
	tenantID, tenantOK := c.Locals("tenant_id").(string)
	userID, userOK := c.Locals("user_id").(int64)
	mobile, mobileOK := c.Locals("mobile").(string)
	epoch, epochOK := c.Locals("session_epoch").(int64)
	if !tenantOK || !userOK || !mobileOK || !epochOK ||
		command.TenantID != tenantID || command.UserID != userID ||
		command.Mobile != mobile || command.SessionEpoch != epoch {
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"code": "session_identity_mismatch", "message": "Session identity does not match"})
	}
	return c.SendStatus(http.StatusNoContent)
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
