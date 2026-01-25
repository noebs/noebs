package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const (
	googleProvider = "google"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleUserURL  = "https://openidconnect.googleapis.com/v1/userinfo"
)

type googleAuthRequest struct {
	Code         string `json:"code" binding:"required"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
}

type googleTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

type googleUserInfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
}

// GoogleAuth exchanges an OAuth code for tokens, then logs in or creates the user.
func (s *Service) GoogleAuth(c *fiber.Ctx) error {
	var req googleAuthRequest
	if err := bindJSON(c, &req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	if req.Code == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "missing_code", "message": "code is required"})
	}
	if s.NoebsConfig.GoogleClientID == "" {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"code": "missing_google_client", "message": "google client id not configured"})
	}

	token, err := s.exchangeGoogleCode(c.Context(), req)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "token_exchange_failed", "message": err.Error()})
	}

	info, err := s.fetchGoogleUserInfo(c.Context(), token.AccessToken)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "userinfo_failed", "message": err.Error()})
	}
	if info.Sub == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "invalid_userinfo", "message": "google subject missing"})
	}

	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, isNew, err := s.findOrCreateUserFromGoogle(c.UserContext(), tenantID, info)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"code": "user_create_failed", "message": err.Error()})
	}

	jwtToken, err := s.Auth.GenerateJWT(user.ID, user.Mobile, tenantID)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"code": "jwt_failed", "message": err.Error()})
	}
	c.Set("Authorization", jwtToken)
	safeUser := sanitizeUser(user)
	return c.Status(http.StatusOK).JSON(fiber.Map{"authorization": jwtToken, "user": safeUser, "new_user": isNew})
}

type completeProfileRequest struct {
	Mobile   string `json:"mobile" binding:"required,len=10"`
	Fullname string `json:"fullname,omitempty"`
}

// CompleteProfile allows a user to attach a mobile number after social signup.
func (s *Service) CompleteProfile(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID == 0 {
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"code": "unauthorized", "message": "missing user id"})
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)

	var req completeProfileRequest
	if err := bindJSON(c, &req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	req.Mobile = strings.TrimSpace(req.Mobile)
	if req.Mobile == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "mobile_required", "message": "mobile is required"})
	}

	if existing, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, req.Mobile); err == nil && existing.ID != userID {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "mobile_taken", "message": "mobile already in use"})
	}
	if err := s.Store.UpdateUserMobile(c.UserContext(), tenantID, userID, req.Mobile, req.Fullname); err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}
	user, err := s.Store.FindUserByID(c.UserContext(), tenantID, userID)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}

	jwtToken, err := s.Auth.GenerateJWT(user.ID, user.Mobile, tenantID)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"code": "jwt_failed", "message": err.Error()})
	}
	c.Set("Authorization", jwtToken)
	safeUser := sanitizeUser(*user)
	return c.Status(http.StatusOK).JSON(fiber.Map{"authorization": jwtToken, "user": safeUser})
}

// AuthMe returns the current user by token.
func (s *Service) AuthMe(c *fiber.Ctx) error {
	userID := getUserID(c)
	if userID == 0 {
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"code": "unauthorized", "message": "missing user id"})
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.FindUserByID(c.UserContext(), tenantID, userID)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}
	safeUser := sanitizeUser(*user)
	return c.Status(http.StatusOK).JSON(fiber.Map{"user": safeUser})
}

func (s *Service) exchangeGoogleCode(ctx context.Context, req googleAuthRequest) (googleTokenResponse, error) {
	var token googleTokenResponse

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", req.Code)
	form.Set("client_id", s.NoebsConfig.GoogleClientID)
	if s.NoebsConfig.GoogleClientSecret != "" {
		form.Set("client_secret", s.NoebsConfig.GoogleClientSecret)
	}
	redirectURI := req.RedirectURI
	if redirectURI == "" {
		redirectURI = s.NoebsConfig.GoogleRedirectURL
	}
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	if req.CodeVerifier != "" {
		form.Set("code_verifier", req.CodeVerifier)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return token, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return token, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return token, fmt.Errorf("google token exchange failed: %s", string(body))
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return token, err
	}
	if token.AccessToken == "" {
		return token, errors.New("missing access_token from google")
	}
	return token, nil
}

func (s *Service) fetchGoogleUserInfo(ctx context.Context, accessToken string) (googleUserInfo, error) {
	var info googleUserInfo
	if accessToken == "" {
		return info, errors.New("missing access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserURL, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("google userinfo failed: %s", string(body))
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return info, err
	}
	return info, nil
}

func (s *Service) findOrCreateUserFromGoogle(ctx context.Context, tenantID string, info googleUserInfo) (ebs_fields.User, bool, error) {
	var user ebs_fields.User
	isNew := false
	if tenantID == "" {
		tenantID = store.DefaultTenantID
	}

	if account, err := s.Store.FindAuthAccount(ctx, tenantID, googleProvider, info.Sub); err == nil {
		found, err := s.Store.FindUserByID(ctx, tenantID, account.UserID)
		if err != nil {
			return user, false, err
		}
		return *found, false, nil
	}

	email := strings.ToLower(info.Email)
	if email != "" {
		if existing, err := s.Store.FindUserByEmail(ctx, tenantID, email); err == nil {
			account := ebs_fields.AuthAccount{
				UserID:         existing.ID,
				Provider:       googleProvider,
				ProviderUserID: info.Sub,
				Email:          email,
				EmailVerified:  info.EmailVerified,
			}
			if err := s.Store.LinkAuthAccount(ctx, tenantID, &account); err != nil {
				return user, false, err
			}
			return *existing, false, nil
		}
	}

	isNew = true
	placeholderMobile := fmt.Sprintf("google:%s", info.Sub)
	password := uuid.NewString()
	user = ebs_fields.User{
		Email:      email,
		Fullname:   info.Name,
		Username:   email,
		Mobile:     placeholderMobile,
		Password:   password,
		IsVerified: false,
	}
	if user.Username == "" {
		user.Username = fmt.Sprintf("google_%s", info.Sub)
	}
	if err := user.HashPassword(); err != nil {
		return user, isNew, err
	}
	if err := s.Store.CreateUser(ctx, tenantID, &user); err != nil {
		return user, isNew, err
	}
	account := ebs_fields.AuthAccount{
		UserID:         user.ID,
		Provider:       googleProvider,
		ProviderUserID: info.Sub,
		Email:          email,
		EmailVerified:  info.EmailVerified,
	}
	if err := s.Store.LinkAuthAccount(ctx, tenantID, &account); err != nil {
		return user, isNew, err
	}
	return user, isNew, nil
}
