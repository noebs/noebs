package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const accountDisplayNamePath = "/internal/identity-auth/accounts/display-name"

type accountDisplayNameResolver interface {
	AccountDisplayName(context.Context, tenantauth.Principal, int64, int64, string, string) (string, error)
}

func (r *identityProfileProjectionResolver) AccountDisplayName(ctx context.Context, principal tenantauth.Principal, actorUserID, targetUserID int64, requestID, sourceIP string) (string, error) {
	if r == nil || r.client == nil || r.endpoint == "" || r.signers == nil || ctx == nil || actorUserID <= 0 || targetUserID <= 0 {
		return "", errProfileProjectionUnavailable
	}
	body, err := json.Marshal(consumer.AccountDisplayNameRequest{UserID: targetUserID})
	if err != nil {
		return "", errProfileProjectionUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+accountDisplayNamePath, bytes.NewReader(body))
	if err != nil {
		return "", errProfileProjectionUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workloadauth.HeaderRequestID, requestID)
	if err := setGatewayPrincipalHeaders(req.Header, principal, "", actorUserID, sourceIP); err != nil {
		return "", errProfileProjectionUnavailable
	}
	if err := r.signers.Sign(string(serviceRoleIdentityAuth), req, body); err != nil {
		return "", errProfileProjectionUnavailable
	}
	client := *r.client
	client.CheckRedirect = workloadauth.RejectRedirect
	response, err := client.Do(req)
	if err != nil {
		return "", errProfileProjectionUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return "", errProfileProjectionNotFound
	}
	if response.StatusCode != http.StatusOK {
		return "", errProfileProjectionUnavailable
	}
	var result consumer.AccountDisplayNameResult
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result.DisplayName == "" || result.DisplayName != strings.TrimSpace(result.DisplayName) {
		return "", errProfileProjectionUnavailable
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errProfileProjectionUnavailable
	}
	return result.DisplayName, nil
}

// Enrichment happens after the wallet service has validated the opaque
// recipient/owned receiving reference. The original actor remains the signed
// caller; the recipient's canonical owner is a separate request body field.
func gatewayAccountNameHandler(spec gatewayRouteSpec, resolver accountDisplayNameResolver, upstream fiber.Handler) fiber.Handler {
	funding := spec.method == http.MethodGet && spec.path == "/wallet/funding-methods"
	statusView := spec.method == http.MethodGet && spec.path == "/wallet/p2p/status"
	p2p := (spec.method == http.MethodPost && spec.path == "/wallet/p2p/preview") ||
		statusView
	if !funding && !p2p {
		return upstream
	}
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		principal, ok := gateway.OIDCPrincipal(c)
		actorID, err := strconv.ParseInt(c.Get(workloadauth.HeaderUserID), 10, 64)
		if !ok || err != nil || actorID <= 0 || resolver == nil {
			return fiber.NewError(http.StatusUnauthorized, "verified account principal is required")
		}
		requestID, sourceIP := c.Get(workloadauth.HeaderRequestID), c.Get(workloadauth.HeaderSourceIP)
		if err := upstream(c); err != nil {
			return err
		}
		if c.Response().StatusCode() != http.StatusOK {
			return nil
		}
		var response map[string]json.RawMessage
		if json.Unmarshal(c.Response().Body(), &response) != nil {
			return fiber.NewError(http.StatusBadGateway, "invalid account response")
		}
		lookup := func(ownerID int64) (json.RawMessage, error) {
			name, err := resolver.AccountDisplayName(c.UserContext(), principal, actorID, ownerID, requestID, sourceIP)
			if err != nil {
				return nil, fiber.NewError(http.StatusBadGateway, "account display name is temporarily unavailable")
			}
			return json.Marshal(name)
		}
		if funding {
			var methods []map[string]json.RawMessage
			if json.Unmarshal(response["methods"], &methods) != nil {
				return fiber.NewError(http.StatusBadGateway, "invalid receiving details")
			}
			var ownName json.RawMessage
			changed := false
			for _, method := range methods {
				if rawJSONString(method["id"]) != "noebs:receive" || rawJSONString(method["provider_id"]) != "noebs" ||
					rawJSONString(method["mode"]) != "account_transfer" || string(method["available"]) != "true" {
					continue
				}
				id, err := uuid.Parse(rawJSONString(method["account_identifier"]))
				if err != nil || id == uuid.Nil {
					return fiber.NewError(http.StatusBadGateway, "invalid receiving reference")
				}
				if ownName == nil {
					ownName, err = lookup(actorID)
					if err != nil {
						return err
					}
				}
				method["account_name"], changed = ownName, true
			}
			if !changed {
				return nil
			}
			response["methods"], err = json.Marshal(methods)
			if err != nil {
				return fiber.NewError(http.StatusBadGateway, "invalid receiving details")
			}
		} else {
			ownerID, err := strconv.ParseInt(rawJSONString(response["to_owner_id"]), 10, 64)
			if err != nil || ownerID <= 0 {
				if statusView {
					return nil
				}
				return fiber.NewError(http.StatusBadGateway, "invalid recipient owner")
			}
			name, err := lookup(ownerID)
			if err != nil {
				if statusView {
					// Mutable display metadata must never hide durable settlement.
					return nil
				}
				return err
			}
			response["recipient_name"] = name
		}
		return c.JSON(response)
	}
}

func rawJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
