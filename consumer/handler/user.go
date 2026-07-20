package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

const (
	maxKYCRequestBodyBytes = 4 * 1024 * 1024
	maxKYCImageBytes       = 2 * 1024 * 1024
)

func (h *Handler) AddDeviceToken(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID <= 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "missing authenticated user", "code": "unauthorized"})
	}
	type data struct {
		Token string `json:"token"`
	}
	var req data
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "device token is required", "code": "invalid_device_token"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.AddDeviceToken(c.UserContext(), tenantID, userID, req.Token); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	return jsonResponse(c, http.StatusOK, nil)
}

func (h *Handler) NecToName(c *fiber.Ctx) error {
	nec := strings.TrimSpace(c.Query("nec"))
	if nec == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "missing nec", "code": "bad_request"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	name, err := h.Service.NecToName(c.UserContext(), tenantID, nec)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "No user found with this NEC", "code": "nec_not_found"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": name})
}

func (h *Handler) GetUser(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID <= 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "missing authenticated user", "code": "unauthorized"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	profile, err := h.Service.GetUserProfile(c.UserContext(), tenantID, userID)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return jsonResponse(c, http.StatusOK, profile)
}

func (h *Handler) UpdateUser(c *fiber.Ctx) error {
	var profile ebs_fields.UserProfile
	if err := bindJSON(c, &profile); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "binding_error"})
	}
	var err error
	profile, err = normalizeUserProfileInput(profile)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "profile data is invalid", "code": "invalid_profile"})
	}
	userID := getUserID(c)
	if userID <= 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "missing authenticated user", "code": "unauthorized"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.UpdateUserProfile(c.UserContext(), tenantID, userID, profile); err != nil {
		if errors.Is(err, store.ErrProfileContactConflict) {
			return jsonResponse(c, http.StatusConflict, fiber.Map{"code": "profile_contact_conflict", "message": "profile contact is already in use"})
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func normalizeUserProfileInput(profile ebs_fields.UserProfile) (ebs_fields.UserProfile, error) {
	profile.Fullname = strings.TrimSpace(profile.Fullname)
	profile.Username = strings.TrimSpace(profile.Username)
	profile.Email = strings.ToLower(strings.TrimSpace(profile.Email))
	profile.Birthday = strings.TrimSpace(profile.Birthday)
	profile.Gender = strings.TrimSpace(profile.Gender)
	if profile.Fullname == "" && profile.Username == "" && profile.Email == "" && profile.Birthday == "" && profile.Gender == "" {
		return profile, store.ErrMissingData
	}
	return profile, nil
}

func (h *Handler) GetUserLanguage(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID <= 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "missing authenticated user", "code": "unauthorized"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	lang, err := h.Service.GetUserLanguage(c.UserContext(), tenantID, userID)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"language": lang})
}

func (h *Handler) SetUserLanguage(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID <= 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "missing authenticated user", "code": "unauthorized"})
	}
	language := strings.TrimSpace(c.Query("language"))
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if language == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "You must set a language", "code": "client_error"})
	}
	if err := h.Service.SetUserLanguage(c.UserContext(), tenantID, userID, language); err != nil {
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func (h *Handler) KYC(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID <= 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "missing authenticated user", "code": "unauthorized"})
	}
	if len(c.Body()) > maxKYCRequestBodyBytes {
		return jsonResponse(c, http.StatusRequestEntityTooLarge, fiber.Map{"message": "KYC request body is too large", "code": "payload_too_large"})
	}
	var req ebs_fields.KYCPassport
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if len(req.Selfie) > maxKYCImageBytes || len(req.PassportImg) > maxKYCImageBytes {
		return jsonResponse(c, http.StatusRequestEntityTooLarge, fiber.Map{"message": "KYC image is too large", "code": "image_too_large"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.UpdateKYC(c.UserContext(), tenantID, userID, req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"message": "KYC created successfully", "code": "ok"})
}

func (h *Handler) TransactionByUUID(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID <= 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "unauthorized", "message": "missing authenticated user identity"})
	}
	id := strings.TrimSpace(c.Query("uuid"))
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	response, err := h.Service.GetTransactionByUUIDForUser(c.UserContext(), tenantID, userID, id)
	if err != nil {
		if errors.Is(err, consumer.ErrTransactionNotFound) {
			return jsonResponse(c, http.StatusNotFound, fiber.Map{"code": "not_found", "message": consumer.ErrTransactionNotFound.Error()})
		}
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, response)
}
