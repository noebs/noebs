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
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	chat "github.com/tutipay/ws"
)

const maxChatContacts = 50

type contactIdentityResolver interface {
	Resolve(context.Context, string, []string) ([]consumer.IdentityUserByMobileResult, error)
}

type identityContactResolver struct {
	endpoint string
	client   *http.Client
	signers  *workloadauth.SignerSet
}

type chatContactRequest struct {
	Name   string `json:"name"`
	Mobile string `json:"mobile"`
}

type chatContactResponse struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Mobile string `json:"mobile"`
}

var chatContactsResolver contactIdentityResolver

func newIdentityContactResolver(cfg ebs_fields.NoebsConfig, signers *workloadauth.SignerSet) (*identityContactResolver, error) {
	if signers == nil {
		return nil, workloadauth.ErrMissingSigner
	}
	endpoint, err := serviceDiscoveryEndpoint(cfg, serviceRoleIdentityAuth)
	if err != nil {
		return nil, err
	}
	return &identityContactResolver{
		endpoint: endpoint,
		client: httpclient.New(
			httpclient.WithTimeout(3*time.Second),
			httpclient.WithResponseHeaderTimeout(2*time.Second),
		),
		signers: signers,
	}, nil
}

func (r *identityContactResolver) Resolve(ctx context.Context, tenantID string, mobiles []string) ([]consumer.IdentityUserByMobileResult, error) {
	if r == nil || r.client == nil || r.endpoint == "" || r.signers == nil {
		return nil, errors.New("identity contact resolver unavailable")
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if len(mobiles) == 0 || len(mobiles) > maxChatContacts {
		return nil, store.ErrInvalidMobile
	}
	payload, err := json.Marshal(consumer.IdentityUsersBatchCommand{Mobiles: mobiles})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/internal/identity-auth/users/resolve-batch", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workloadauth.HeaderRequestID, uuid.NewString())
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	if err := r.signers.Sign(string(serviceRoleIdentityAuth), req, payload); err != nil {
		return nil, err
	}

	client := *r.client
	client.CheckRedirect = workloadauth.RejectRedirect
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("identity contact resolver returned %s", response.Status)
	}
	var result consumer.IdentityUsersBatchResult
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if err := ensureJSONDecoderEOF(decoder); err != nil {
		return nil, err
	}
	requested := make(map[string]struct{}, len(mobiles))
	for _, mobile := range mobiles {
		requested[mobile] = struct{}{}
	}
	seen := make(map[string]struct{}, len(result.Users))
	for _, user := range result.Users {
		if user.UserID <= 0 {
			return nil, store.ErrInvalidUserID
		}
		if _, ok := requested[user.Mobile]; !ok {
			return nil, errors.New("identity contact resolver returned an unrequested mobile")
		}
		if _, duplicate := seen[user.Mobile]; duplicate {
			return nil, errors.New("identity contact resolver returned a duplicate mobile")
		}
		seen[user.Mobile] = struct{}{}
	}
	return result.Users, nil
}

func submitChatContacts(c *fiber.Ctx, resolver contactIdentityResolver, db *sqlx.DB) error {
	tenantID, ok := c.Locals("tenant_id").(string)
	if !ok {
		return chatContactError(c, http.StatusUnauthorized, "missing_gateway_identity")
	}
	userID, ok := c.Locals("user_id").(int64)
	if !ok || userID <= 0 {
		return chatContactError(c, http.StatusUnauthorized, "missing_gateway_identity")
	}
	contacts, err := decodeChatContacts(c.Body())
	if err != nil {
		return chatContactError(c, http.StatusBadRequest, "invalid_contacts")
	}
	if resolver == nil {
		return chatContactError(c, http.StatusServiceUnavailable, "contact_resolution_unavailable")
	}
	mobiles := make([]string, 0, len(contacts))
	byMobile := make(map[string]chatContactRequest, len(contacts))
	for _, contact := range contacts {
		mobiles = append(mobiles, contact.Mobile)
		byMobile[contact.Mobile] = contact
	}
	resolved, err := resolver.Resolve(c.UserContext(), tenantID, mobiles)
	if err != nil {
		return chatContactError(c, http.StatusServiceUnavailable, "contact_resolution_unavailable")
	}

	contactIDs := make([]int64, 0, len(resolved))
	response := make([]chatContactResponse, 0, len(resolved))
	for _, user := range resolved {
		if user.UserID == userID {
			continue
		}
		contact := byMobile[user.Mobile]
		contactIDs = append(contactIDs, user.UserID)
		response = append(response, chatContactResponse{UserID: user.UserID, Name: contact.Name, Mobile: user.Mobile})
	}
	if len(contactIDs) > 0 {
		if err := chat.AddContacts(c.UserContext(), chat.ClientIdentity{TenantID: tenantID, UserID: userID}, contactIDs, db); err != nil {
			if errors.Is(err, chat.ErrInvalidContactBatch) || errors.Is(err, chat.ErrInvalidClientIdentity) {
				return chatContactError(c, http.StatusBadRequest, "invalid_contacts")
			}
			return chatContactError(c, http.StatusServiceUnavailable, "chat_persistence_unavailable")
		}
	}
	return c.Status(http.StatusOK).JSON(response)
}

func decodeChatContacts(body []byte) ([]chatContactRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var input []chatContactRequest
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	if err := ensureJSONDecoderEOF(decoder); err != nil {
		return nil, err
	}
	if len(input) == 0 || len(input) > maxChatContacts {
		return nil, store.ErrInvalidMobile
	}
	result := make([]chatContactRequest, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, contact := range input {
		contact.Mobile = strings.TrimSpace(contact.Mobile)
		contact.Name = strings.TrimSpace(contact.Name)
		if !validChatMobile(contact.Mobile) || len(contact.Name) > 200 {
			return nil, store.ErrInvalidMobile
		}
		if _, exists := seen[contact.Mobile]; exists {
			continue
		}
		seen[contact.Mobile] = struct{}{}
		result = append(result, contact)
	}
	return result, nil
}

func validChatMobile(mobile string) bool {
	if len(mobile) != 10 {
		return false
	}
	for index := range mobile {
		if mobile[index] < '0' || mobile[index] > '9' {
			return false
		}
	}
	return true
}

func ensureJSONDecoderEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func chatContactError(c *fiber.Ctx, status int, code string) error {
	return c.Status(status).JSON(fiber.Map{"code": code, "message": "contact sync failed"})
}
