package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

func (h *Handler) GenerateAPIKey(c *fiber.Ctx) error {
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
	var req ebs_fields.User
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	source, err := resolveRequestSource(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_request_source"})
	}
	token, user, err := h.Service.Login(c.UserContext(), tenantID, req.Mobile, req.Password, source, time.Now().UTC())
	if err != nil {
		if errors.Is(err, consumer.ErrRateLimited) {
			return rateLimitResponse(c, err)
		}
		if store.ErrNotFound(err) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "not_found"})
		}
		if errors.Is(err, consumer.ErrWrongPassword) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "wrong password entered", "code": "wrong_password"})
		}
		if errors.Is(err, consumer.ErrUserNotVerified) {
			return jsonResponse(c, http.StatusForbidden, fiber.Map{"message": "Verify your mobile number before signing in", "code": "user_not_verified"})
		}
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"authorization": token, "user": user})
}

func (h *Handler) SingleLoginHandler(c *fiber.Ctx) error {
	var req gateway.Token
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	source, err := resolveRequestSource(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_request_source"})
	}

	token, user, err := h.Service.SingleLogin(c.UserContext(), tenantID, req, source, time.Now().UTC())
	if err != nil {
		if errors.Is(err, consumer.ErrRateLimited) {
			return rateLimitResponse(c, err)
		}
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
	var req gateway.Token
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	source, err := resolveRequestSource(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_request_source"})
	}

	token, err := h.Service.RefreshJWT(c.UserContext(), tenantID, req, source, time.Now().UTC())
	if err != nil {
		if errors.Is(err, consumer.ErrRateLimited) {
			return rateLimitResponse(c, err)
		}
		if errors.Is(err, consumer.ErrRefreshReplay) {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "Refresh token has already been used", "code": "refresh_replay"})
		}
		if errors.Is(err, consumer.ErrRefreshExpired) {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "Refresh window has expired", "code": "refresh_expired"})
		}
		if errors.Is(err, consumer.ErrRefreshTenantMismatch) {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "Token tenant does not match request tenant", "code": "tenant_mismatch"})
		}
		if errors.Is(err, consumer.ErrSessionRevoked) {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"message": "Session has been revoked", "code": "session_revoked"})
		}
		if errors.Is(err, consumer.ErrInvalidSignature) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Invalid signature", "code": "invalid_signature"})
		}
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
	var req consumer.RegisterUserCommand
	if err := bindStrictJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	source, err := resolveRequestSource(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_request_source"})
	}

	user, err := h.Service.CreateUser(c.UserContext(), tenantID, req, source, time.Now().UTC())
	if err != nil {
		if errors.Is(err, consumer.ErrRateLimited) {
			return rateLimitResponse(c, err)
		}
		if errors.Is(err, consumer.ErrPasswordInvalid) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Password must be at least 8 characters long, and must include at least one capital letter, one symbol and one number", "code": "password_invalid"})
		}
		if errors.Is(err, consumer.ErrMissingPublicKey) || errors.Is(err, consumer.ErrInvalidPublicKey) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "A valid RSA public key is required", "code": "invalid_public_key"})
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "duplicate_username"})
	}
	return jsonResponse(c, http.StatusCreated, fiber.Map{
		"ok":      "object was successfully created",
		"details": fiber.Map{"mobile": user.Mobile},
	})
}

func (h *Handler) VerifyOTP(c *fiber.Ctx) error {
	var req struct {
		Mobile    string `json:"mobile"`
		OTP       string `json:"otp"`
		Signature string `json:"signature"`
	}
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

	source, err := resolveRequestSource(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_request_source"})
	}

	user, err := h.Service.VerifyOTP(c.UserContext(), tenantID, req.Mobile, req.OTP, req.Signature, source, time.Now().UTC())
	if err != nil {
		if errors.Is(err, consumer.ErrRateLimited) {
			return rateLimitResponse(c, err)
		}
		if store.ErrNotFound(err) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "not_found"})
		}
		if errors.Is(err, consumer.ErrInvalidOTP) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Invalid otp", "code": "invalid_otp"})
		}
		if errors.Is(err, consumer.ErrInvalidSignature) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Invalid signature", "code": "invalid_signature"})
		}
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok", "user": user, "pubkey": h.Service.NoebsConfig.EBSConsumerKey})
}

func (h *Handler) BalanceStep(c *fiber.Ctx) error {
	var req consumer.BalanceStepRequest
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	credential, err := h.Service.BalanceStep(c.UserContext(), tenantID, req)
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
	preventCredentialCaching(c)
	return jsonResponse(c, http.StatusOK, credential)
}

func (h *Handler) ChangePassword(c *fiber.Ctx) error {
	mobile := getMobile(c)
	var req struct {
		CurrentPassword string `json:"password"`
		NewPassword string `json:"new_password"`
	}
	if err := parseJSON(c, &req); err != nil || strings.TrimSpace(req.CurrentPassword) == "" || strings.TrimSpace(req.NewPassword) == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Bad request.", "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	token, user, err := h.Service.ChangePassword(c.UserContext(), tenantID, mobile, req.CurrentPassword, req.NewPassword, time.Now().UTC())
	if err != nil {
		if errors.Is(err, consumer.ErrPasswordInvalid) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Password must be at least 8 characters long, and must include at least one capital letter, one symbol and one number", "code": "password_invalid"})
		}
		if errors.Is(err, consumer.ErrWrongPassword) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "wrong password entered", "code": "wrong_password"})
		}
		if store.ErrNotFound(err) {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "not_found"})
		}
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	c.Set("Authorization", token)
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok", "authorization": token, "user": user})
}

func (h *Handler) GenerateSignInCode(c *fiber.Ctx) error {
	return h.generateSignInCode(c)
}

func (h *Handler) generateSignInCode(c *fiber.Ctx) error {
	var req gateway.Token
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if strings.TrimSpace(req.Mobile) == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Mobile number was not sent", "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	source, err := resolveRequestSource(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_request_source"})
	}

	if err := h.Service.GenerateSignInCode(c.UserContext(), tenantID, req.Mobile, source, time.Now().UTC()); err != nil {
		if errors.Is(err, consumer.ErrRateLimited) {
			return rateLimitResponse(c, err)
		}
		status, body := generateSignInCodeErrorResponse(err)
		return jsonResponse(c, status, body)
	}
	return jsonResponse(c, http.StatusCreated, fiber.Map{"status": "ok", "message": "If the account is awaiting verification, a code will be sent."})
}

func rateLimitResponse(c *fiber.Ctx, err error) error {
	retryAfter := time.Second
	var limitErr *consumer.RateLimitError
	if errors.As(err, &limitErr) && limitErr.RetryAfter > 0 {
		retryAfter = limitErr.RetryAfter
	}
	seconds := int64((retryAfter + time.Second - 1) / time.Second)
	c.Set(fiber.HeaderRetryAfter, strconv.FormatInt(seconds, 10))
	return jsonResponse(c, http.StatusTooManyRequests, fiber.Map{
		"message": "Too many authentication attempts. Try again later.",
		"code":    "rate_limited",
	})
}

func generateSignInCodeErrorResponse(err error) (int, fiber.Map) {
	switch {
	case errors.Is(err, consumer.ErrMissingStore):
		return http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable", "message": err.Error()}
	case errors.Is(err, store.ErrMissingTenantID), errors.Is(err, store.ErrInvalidTenantID):
		return http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()}
	default:
		return http.StatusInternalServerError, fiber.Map{"message": "Verification is temporarily unavailable", "code": "service_error"}
	}
}
