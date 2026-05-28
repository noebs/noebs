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
	"github.com/golang-jwt/jwt/v5"
)

func (h *Handler) GenerateAPIKey(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req map[string]string
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "bad_request"})
	}
	email := strings.TrimSpace(req["email"])
	if email == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "missing_field"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	key, err := h.Service.GenerateAPIKey(c.UserContext(), tenantID, email)
	if err != nil {
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": "db_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": key})
}

func (h *Handler) LoginHandler(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req ebs_fields.User
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	token, user, err := h.Service.Login(c.UserContext(), tenantID, req.Mobile, req.Password)
	if err != nil {
		if store.ErrNotFound(err) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "not_found"})
		}
		if errors.Is(err, consumer.ErrWrongPassword) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "wrong password entered", "code": "wrong_password"})
		}
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"authorization": token, "user": user})
}

func (h *Handler) SingleLoginHandler(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req gateway.Token
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	token, user, err := h.Service.SingleLogin(c.UserContext(), tenantID, req)
	if err != nil {
		if store.ErrNotFound(err) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "not_found"})
		}
		if errors.Is(err, consumer.ErrWrongOTP) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "wrong otp entered", "code": "wrong_otp"})
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"authorization": token, "user": user})
}

func (h *Handler) RefreshHandler(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req gateway.Token
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}

	token, err := h.Service.RefreshJWT(c.UserContext(), req)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "Token has expired", "code": "jwt_expired"})
		}
		if errors.Is(err, store.ErrMissingTenantID) || errors.Is(err, store.ErrInvalidTenantID) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Malformed token", "code": "jwt_malformed"})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"authorization": token})
}

func (h *Handler) CreateUser(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req ebs_fields.User
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	user, err := h.Service.CreateUser(c.UserContext(), tenantID, req)
	if err != nil {
		if errors.Is(err, consumer.ErrPasswordInvalid) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Password must be at least 8 characters long, and must include at least one capital letter, one symbol and one number", "code": "password_invalid"})
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "duplicate_username"})
	}
	return jsonResponse(c, http.StatusCreated, fiber.Map{"ok": "object was successfully created", "details": user})
}

func (h *Handler) VerifyOTP(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req ebs_fields.User
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if req.OTP == "" || req.Mobile == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "otp was not sent", "code": "empty_otp"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	user, err := h.Service.VerifyOTP(c.UserContext(), tenantID, req.Mobile, req.OTP)
	if err != nil {
		if store.ErrNotFound(err) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "not_found"})
		}
		if errors.Is(err, consumer.ErrInvalidOTP) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Invalid otp", "code": "invalid_otp"})
		}
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok", "user": user, "pubkey": h.Service.NoebsConfig.EBSConsumerKey})
}

func (h *Handler) BalanceStep(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req consumer.BalanceStepRequest
	_ = parseJSON(c, &req)

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	token, err := h.Service.BalanceStep(c.UserContext(), tenantID, req)
	if err != nil {
		code := "bad_request"
		msg := err.Error()
		switch {
		case errors.Is(err, consumer.ErrCardNotMatched):
			code = "card_not_matched"
			msg = "no matching card was found"
		case errors.Is(err, consumer.ErrTransactionFailed):
			code = "transaction_failed"
			msg = "Invalid credentials"
		}
		return jsonResponse(c, statusForError(err), fiber.Map{"message": msg, "code": code})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok", "authorization": token})
}

func (h *Handler) ChangePassword(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	var req ebs_fields.User
	if err := bindJSON(c, &req); err != nil || req.NewPassword == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Bad request.", "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	user, err := h.Service.ChangePassword(c.UserContext(), tenantID, mobile, req.NewPassword)
	if err != nil {
		if store.ErrNotFound(err) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "not_found"})
		}
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok", "user": user})
}

func (h *Handler) GenerateSignInCode(c *fiber.Ctx) error {
	return h.generateSignInCode(c)
}

func (h *Handler) generateSignInCode(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req gateway.Token
	_ = parseJSON(c, &req)
	if strings.TrimSpace(req.Mobile) == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Mobile number was not sent", "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	if err := h.Service.GenerateSignInCode(c.UserContext(), tenantID, req.Mobile); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "user not found", "code": "not_found"})
	}
	return jsonResponse(c, http.StatusCreated, fiber.Map{"status": "ok", "message": "Password reset link has been sent to your mobile number. Use the info to login in to your account."})
}
