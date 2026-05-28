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

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
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
func (s *Service) GoogleAuth(ctx context.Context, tenantID string, code, codeVerifier, redirectURI string) (string, ebs_fields.User, bool, error) {
	var empty ebs_fields.User
	if s == nil || s.Store == nil {
		return "", empty, false, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", empty, false, err
	}
	if strings.TrimSpace(code) == "" {
		return "", empty, false, errors.New("missing_code")
	}
	if s.NoebsConfig.GoogleClientID == "" {
		return "", empty, false, errors.New("missing_google_client")
	}
	if s.HTTPClient == nil {
		return "", empty, false, ErrMissingHTTPClient
	}

	req := googleAuthRequest{Code: code, CodeVerifier: codeVerifier, RedirectURI: redirectURI}
	token, err := s.exchangeGoogleCode(ctx, req)
	if err != nil {
		return "", empty, false, err
	}
	info, err := s.fetchGoogleUserInfo(ctx, token.AccessToken)
	if err != nil {
		return "", empty, false, err
	}
	if strings.TrimSpace(info.Sub) == "" {
		return "", empty, false, errors.New("invalid_userinfo")
	}

	user, isNew, err := s.findOrCreateUserFromGoogle(ctx, tenantID, info)
	if err != nil {
		return "", empty, false, err
	}

	jwtToken, err := s.Auth.GenerateJWT(user.ID, user.Mobile, tenantID)
	if err != nil {
		return "", empty, false, err
	}
	return jwtToken, sanitizeUser(user), isNew, nil
}

// CompleteProfile allows a user to attach a mobile number after social signup.
func (s *Service) CompleteProfile(ctx context.Context, tenantID string, userID int64, mobile, fullname string) (string, ebs_fields.User, error) {
	var empty ebs_fields.User
	if s == nil || s.Store == nil {
		return "", empty, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", empty, err
	}
	if userID <= 0 {
		return "", empty, store.ErrInvalidUserID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return "", empty, errors.New("mobile_required")
	}

	if existing, err := s.Store.GetUserByMobile(ctx, tenantID, mobile); err == nil && existing.ID != userID {
		return "", empty, errors.New("mobile_taken")
	}
	if err := s.Store.UpdateUserMobile(ctx, tenantID, userID, mobile, fullname); err != nil {
		return "", empty, err
	}
	user, err := s.Store.FindUserByID(ctx, tenantID, userID)
	if err != nil {
		return "", empty, err
	}

	jwtToken, err := s.Auth.GenerateJWT(user.ID, user.Mobile, tenantID)
	if err != nil {
		return "", empty, err
	}
	return jwtToken, sanitizeUser(*user), nil
}

// AuthMe returns the current user by token.
func (s *Service) AuthMe(ctx context.Context, tenantID string, userID int64) (ebs_fields.User, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.User{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.User{}, err
	}
	if userID <= 0 {
		return ebs_fields.User{}, store.ErrInvalidUserID
	}
	user, err := s.Store.FindUserByID(ctx, tenantID, userID)
	if err != nil {
		return ebs_fields.User{}, err
	}
	return sanitizeUser(*user), nil
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

	resp, err := s.HTTPClient.Do(httpReq)
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
	resp, err := s.HTTPClient.Do(req)
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
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return user, false, err
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
			_ = s.Store.LinkAuthAccount(ctx, tenantID, &account)
			return *existing, false, nil
		}
	}

	// Create a new local user with an internal mobile placeholder until profile completion.
	mobile := fmt.Sprintf("google:%s", info.Sub)
	user = ebs_fields.User{
		Mobile:     mobile,
		Username:   mobile,
		Fullname:   info.Name,
		Email:      email,
		IsVerified: true,
	}
	user.Password = uuid.New().String()
	_ = user.HashPassword()
	if err := s.Store.CreateUser(ctx, tenantID, &user); err != nil {
		return ebs_fields.User{}, false, err
	}
	account := ebs_fields.AuthAccount{
		UserID:         user.ID,
		Provider:       googleProvider,
		ProviderUserID: info.Sub,
		Email:          email,
		EmailVerified:  info.EmailVerified,
	}
	_ = s.Store.LinkAuthAccount(ctx, tenantID, &account)
	isNew = true
	return user, isNew, nil
}
