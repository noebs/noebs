package handler

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
)

type googleAuthRequest struct {
	Code         string `json:"code" binding:"required"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
}

// GoogleAuth exchanges an OAuth code for tokens, then logs in or creates the user.
func (h *Handler) GoogleAuth(c *fiber.Ctx) error {
	var req googleAuthRequest
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	token, user, isNew, err := h.Service.GoogleAuth(c.UserContext(), tenantID, req.Code, req.CodeVerifier, req.RedirectURI)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "auth_failed", "message": err.Error()})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"authorization": token, "user": user, "new_user": isNew})
}

type completeProfileRequest struct {
	Mobile   string `json:"mobile" binding:"required,len=10"`
	Fullname string `json:"fullname,omitempty"`
}

func (h *Handler) CompleteProfile(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID == 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "unauthorized", "message": "missing user id"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var req completeProfileRequest
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}

	token, user, err := h.Service.CompleteProfile(c.UserContext(), tenantID, userID, req.Mobile, req.Fullname)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "profile_failed", "message": err.Error()})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"authorization": token, "user": user})
}

func (h *Handler) AuthMe(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID == 0 {
		return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "unauthorized", "message": "missing user id"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	user, err := h.Service.AuthMe(c.UserContext(), tenantID, userID)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"user": user})
}
