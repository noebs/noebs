package handler

import (
	"errors"
	"net/http"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

type createProfileProjectionRequest struct {
	Fullname    string `json:"fullname" binding:"required"`
	Username    string `json:"username,omitempty"`
	Gender      string `json:"gender,omitempty"`
	Birthday    string `json:"birthday,omitempty"`
	Email       string `json:"email,omitempty"`
	DeviceToken string `json:"device_token,omitempty"`
	Language    string `json:"language,omitempty"`
}

func (h *Handler) CreateProfileProjection(c *fiber.Ctx) error {
	principal, ok := gateway.InternalPrincipalIdentity(c)
	if !ok {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "authentication_failed", "message": "verified principal is required"})
	}
	var request createProfileProjectionRequest
	if err := bindJSON(c, &request); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	request.Fullname = strings.TrimSpace(request.Fullname)
	request.Username = strings.TrimSpace(request.Username)
	request.Gender = strings.TrimSpace(request.Gender)
	request.Birthday = strings.TrimSpace(request.Birthday)
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.DeviceToken = strings.TrimSpace(request.DeviceToken)
	request.Language = strings.TrimSpace(request.Language)
	if err := ebs_fields.ValidateStruct(request); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "invalid_profile", "message": "profile data is invalid"})
	}
	command := consumer.CreateProfileProjectionCommand{
		Fullname: request.Fullname, Username: request.Username, Gender: request.Gender,
		Birthday: request.Birthday, Email: request.Email,
		DeviceToken: request.DeviceToken, Language: request.Language,
	}
	profile, err := h.Service.CreateProfileProjection(c.UserContext(), principal.TenantID, consumer.PrincipalProjectionReference{
		Issuer: principal.Issuer, Subject: principal.Subject,
	}, command)
	if err != nil {
		return profileProjectionError(c, err)
	}
	return jsonResponse(c, http.StatusCreated, fiber.Map{"user": profile})
}

func profileProjectionError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, store.ErrProfileAlreadyExists):
		return jsonResponse(c, http.StatusConflict, fiber.Map{"code": "profile_already_exists", "message": "profile already exists"})
	case errors.Is(err, store.ErrProfileContactConflict):
		return jsonResponse(c, http.StatusConflict, fiber.Map{"code": "profile_contact_conflict", "message": "profile contact is already in use"})
	case errors.Is(err, store.ErrMissingTenantID), errors.Is(err, store.ErrInvalidTenantID),
		errors.Is(err, store.ErrMissingIssuer), errors.Is(err, store.ErrInvalidIssuer),
		errors.Is(err, store.ErrMissingSubject), errors.Is(err, store.ErrInvalidSubject),
		errors.Is(err, store.ErrMissingProfileName), errors.Is(err, store.ErrInvalidProfileName),
		errors.Is(err, store.ErrMissingData):
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "invalid_profile", "message": "profile data is invalid"})
	case errors.Is(err, consumer.ErrMissingStore):
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "profile_service_unavailable", "message": "profile service is unavailable"})
	default:
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"code": "profile_create_failed", "message": "profile could not be created"})
	}
}
