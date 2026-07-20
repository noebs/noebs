package backofficeauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	pathpkg "path"
	"strings"
	"time"
)

const (
	flowVerifierPurpose  = "auth-flow-pkce-verifier"
	sessionTokenPurpose  = "session-oauth-tokens"
	tokenMaterialVersion = 1
)

type ServiceConfig struct {
	Flows            FlowRepository
	Sessions         SessionRepository
	OAuth            *OAuthClient
	Keys             *Keyring
	Cookies          *CookiePolicy
	Clock            Clock
	Entropy          io.Reader
	FlowTTL          time.Duration
	IdleTTL          time.Duration
	AbsoluteTTL      time.Duration
	RefreshSkew      time.Duration
	TouchInterval    time.Duration
	ReturnPathPrefix string
}

type Service struct {
	flows            FlowRepository
	sessions         SessionRepository
	oauth            *OAuthClient
	keys             *Keyring
	cookies          *CookiePolicy
	clock            Clock
	entropy          io.Reader
	flowTTL          time.Duration
	idleTTL          time.Duration
	absoluteTTL      time.Duration
	refreshSkew      time.Duration
	touchInterval    time.Duration
	returnPathPrefix string
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Flows == nil || config.Sessions == nil || config.OAuth == nil || config.Keys == nil ||
		config.Cookies == nil || config.Clock == nil || config.Entropy == nil ||
		!validDuration(config.FlowTTL) || !validDuration(config.IdleTTL) || !validDuration(config.AbsoluteTTL) ||
		!validDuration(config.RefreshSkew) || !validDuration(config.TouchInterval) ||
		config.IdleTTL > config.AbsoluteTTL || config.RefreshSkew >= config.IdleTTL ||
		config.TouchInterval >= config.IdleTTL || !validReturnPathPrefix(config.ReturnPathPrefix) {
		return nil, ErrInvalidConfiguration
	}
	return &Service{
		flows:            config.Flows,
		sessions:         config.Sessions,
		oauth:            config.OAuth,
		keys:             config.Keys,
		cookies:          config.Cookies,
		clock:            config.Clock,
		entropy:          config.Entropy,
		flowTTL:          config.FlowTTL,
		idleTTL:          config.IdleTTL,
		absoluteTTL:      config.AbsoluteTTL,
		refreshSkew:      config.RefreshSkew,
		touchInterval:    config.TouchInterval,
		returnPathPrefix: config.ReturnPathPrefix,
	}, nil
}

func validDuration(value time.Duration) bool {
	return value > 0 && value%time.Second == 0
}

func validReturnPathPrefix(prefix string) bool {
	if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") || strings.ContainsAny(prefix, "\\%?#") {
		return false
	}
	return pathpkg.Clean(strings.TrimSuffix(prefix, "/"))+"/" == prefix
}

func (s *Service) BeginLogin(ctx context.Context, returnPath string) (LoginStart, error) {
	if s == nil || ctx == nil || !s.validReturnPath(returnPath) {
		return LoginStart{}, ErrInvalidInput
	}
	state, err := generateOpaque(s.entropy)
	if err != nil {
		return LoginStart{}, err
	}
	browserBinding, err := generateOpaque(s.entropy)
	if err != nil {
		return LoginStart{}, err
	}
	nonce, err := generateOpaque(s.entropy)
	if err != nil {
		return LoginStart{}, err
	}
	verifier, err := generateOpaque(s.entropy)
	if err != nil {
		return LoginStart{}, err
	}
	stateHash, _ := digestOpaque(state)
	browserHash, _ := digestOpaque(browserBinding)
	sealedVerifier, err := s.keys.Seal(flowVerifierPurpose, stateHash, []byte(verifier))
	if err != nil {
		return LoginStart{}, err
	}
	now := s.clock.Now().UTC()
	expiresAt := now.Add(s.flowTTL)
	flow := FlowRecord{
		StateHash:    stateHash,
		BrowserHash:  browserHash,
		PKCEVerifier: sealedVerifier,
		NonceHash:    digestString(nonce),
		ReturnPath:   returnPath,
		CreatedAt:    now,
		ExpiresAt:    expiresAt,
	}
	authorizationURL, err := s.oauth.authorizationURL(state, nonce, verifier)
	if err != nil {
		return LoginStart{}, err
	}
	cookie, err := s.cookies.Flow(browserBinding, now, expiresAt)
	if err != nil {
		return LoginStart{}, err
	}
	if err := s.flows.CreateFlow(ctx, flow); err != nil {
		return LoginStart{}, err
	}
	return LoginStart{AuthorizationURL: authorizationURL, FlowCookie: cookie}, nil
}

func (s *Service) CompleteLogin(ctx context.Context, state, browserBinding, code string) (LoginComplete, error) {
	if s == nil || ctx == nil || code == "" {
		return LoginComplete{}, ErrInvalidInput
	}
	stateHash, err := digestOpaque(state)
	if err != nil {
		return LoginComplete{}, ErrInvalidFlow
	}
	browserHash, err := digestOpaque(browserBinding)
	if err != nil {
		return LoginComplete{}, ErrInvalidFlow
	}
	now := s.clock.Now().UTC()
	flow, err := s.flows.ConsumeFlow(ctx, stateHash, browserHash, now)
	if err != nil {
		return LoginComplete{}, err
	}
	if !s.validReturnPath(flow.ReturnPath) {
		return LoginComplete{}, ErrInvalidFlow
	}
	verifierBytes, err := s.keys.Open(flowVerifierPurpose, stateHash, flow.PKCEVerifier)
	if err != nil {
		return LoginComplete{}, err
	}
	verifier := string(verifierBytes)
	if _, err := digestOpaque(verifier); err != nil {
		return LoginComplete{}, ErrInvalidFlow
	}
	oauthResult, err := s.oauth.exchange(ctx, code, verifier, flow.NonceHash)
	if err != nil {
		return LoginComplete{}, err
	}
	if len(oauthResult.claims.Memberships()) == 0 {
		return LoginComplete{}, ErrInvalidAccessToken
	}
	sessionID, err := generateOpaque(s.entropy)
	if err != nil {
		return LoginComplete{}, err
	}
	sessionHash, _ := digestOpaque(sessionID)
	csrfSecret, _, err := GenerateCSRFSecret(s.entropy)
	if err != nil {
		return LoginComplete{}, err
	}
	material := tokenMaterial{
		Version:      tokenMaterialVersion,
		AccessToken:  oauthResult.accessToken,
		RefreshToken: oauthResult.refreshToken,
		IDToken:      oauthResult.idToken,
		CSRFSecret:   csrfSecret,
	}
	sealedTokens, err := s.sealTokenMaterial(sessionHash, material)
	if err != nil {
		return LoginComplete{}, err
	}
	absoluteExpiresAt := now.Add(s.absoluteTTL)
	idleExpiresAt := minTime(now.Add(s.idleTTL), absoluteExpiresAt)
	identity := oauthResult.claims.Identity()
	session := SessionRecord{
		SessionHash:       sessionHash,
		Issuer:            identity.Issuer,
		Subject:           identity.Subject,
		Tokens:            sealedTokens,
		AccessExpiresAt:   oauthResult.accessExpiresAt,
		RefreshExpiresAt:  oauthResult.refreshExpiresAt,
		IdleExpiresAt:     idleExpiresAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
		LastSeenAt:        now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	cookie, err := s.cookies.Session(sessionID, now, absoluteExpiresAt)
	if err != nil {
		return LoginComplete{}, err
	}
	if err := s.sessions.CreateSession(ctx, session); err != nil {
		return LoginComplete{}, err
	}
	return LoginComplete{
		SessionCookie:   cookie,
		ClearFlowCookie: s.cookies.ClearFlow(),
		ReturnPath:      flow.ReturnPath,
		Claims:          oauthResult.claims,
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, rawSessionID string) (AuthenticatedSession, error) {
	if s == nil || ctx == nil {
		return AuthenticatedSession{}, ErrInvalidInput
	}
	sessionHash, err := digestOpaque(rawSessionID)
	if err != nil {
		return AuthenticatedSession{}, ErrSessionNotFound
	}
	session, err := s.sessions.LoadSession(ctx, sessionHash)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	now := s.clock.Now().UTC()
	if sessionDeadlinePassed(session, now) {
		session, err = s.sessions.TouchSession(
			ctx,
			sessionHash,
			now,
			minTime(now.Add(s.idleTTL), session.AbsoluteExpiresAt),
			now.Add(-s.touchInterval),
		)
		if err != nil {
			return AuthenticatedSession{}, err
		}
	}
	if !session.AccessExpiresAt.After(now.Add(s.refreshSkew)) {
		session, err = s.sessions.RefreshSession(ctx, sessionHash, s.clock, s.refreshSkew, s.refresh)
		if err != nil {
			return AuthenticatedSession{}, err
		}
	}
	material, err := s.openTokenMaterial(session)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	claims, err := s.oauth.verifyAccessToken(ctx, material.AccessToken, session.Subject)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	identity := claims.Identity()
	if identity.Issuer != session.Issuer || !identity.ExpiresAt.Equal(session.AccessExpiresAt) || len(claims.Memberships()) == 0 {
		return AuthenticatedSession{}, ErrInvalidAccessToken
	}
	now = s.clock.Now().UTC()
	if !identity.ExpiresAt.After(now) {
		return AuthenticatedSession{}, ErrSessionExpired
	}
	csrfToken, err := CSRFToken(material.CSRFSecret)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	session, err = s.sessions.TouchSession(
		ctx,
		sessionHash,
		now,
		minTime(now.Add(s.idleTTL), session.AbsoluteExpiresAt),
		now.Add(-s.touchInterval),
	)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	return AuthenticatedSession{
		Claims:            claims,
		CSRFToken:         csrfToken,
		IdleExpiresAt:     session.IdleExpiresAt,
		AbsoluteExpiresAt: session.AbsoluteExpiresAt,
	}, nil
}

func sessionDeadlinePassed(session SessionRecord, now time.Time) bool {
	return !session.RefreshExpiresAt.After(now) || !session.IdleExpiresAt.After(now) || !session.AbsoluteExpiresAt.After(now)
}

func (s *Service) refresh(ctx context.Context, session SessionRecord) (SessionRefresh, error) {
	material, err := s.openTokenMaterial(session)
	if err != nil {
		return SessionRefresh{}, err
	}
	result, err := s.oauth.refresh(
		ctx,
		material.AccessToken,
		material.RefreshToken,
		material.IDToken,
		session.Subject,
	)
	if err != nil {
		return SessionRefresh{}, err
	}
	material.AccessToken = result.accessToken
	material.RefreshToken = result.refreshToken
	material.IDToken = result.idToken
	sealed, err := s.sealTokenMaterial(session.SessionHash, material)
	if err != nil {
		return SessionRefresh{}, err
	}
	return SessionRefresh{
		Tokens:           sealed,
		AccessExpiresAt:  result.accessExpiresAt,
		RefreshExpiresAt: result.refreshExpiresAt,
	}, nil
}

func (s *Service) Logout(ctx context.Context, rawSessionID string) (LogoutComplete, error) {
	if s == nil || ctx == nil {
		return LogoutComplete{}, ErrInvalidInput
	}
	clear := s.cookies.ClearSession()
	sessionHash, err := digestOpaque(rawSessionID)
	if err != nil {
		return LogoutComplete{ClearSessionCookie: clear}, ErrSessionNotFound
	}
	session, err := s.sessions.LoadSession(ctx, sessionHash)
	if err != nil {
		return LogoutComplete{ClearSessionCookie: clear}, err
	}
	material, err := s.openTokenMaterial(session)
	if err != nil {
		return LogoutComplete{ClearSessionCookie: clear}, err
	}
	endSessionURL, err := s.oauth.logoutURL(material.IDToken)
	if err != nil {
		return LogoutComplete{ClearSessionCookie: clear}, err
	}
	deleted, err := s.sessions.DeleteSession(ctx, sessionHash)
	if err != nil {
		return LogoutComplete{ClearSessionCookie: clear}, err
	}
	if !deleted {
		return LogoutComplete{ClearSessionCookie: clear}, ErrSessionNotFound
	}
	return LogoutComplete{ClearSessionCookie: clear, EndSessionURL: endSessionURL}, nil
}

func (s *Service) validReturnPath(raw string) bool {
	if len(raw) > 2048 || !strings.HasPrefix(raw, s.returnPathPrefix) || strings.ContainsAny(raw, "\\%#?") {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.RawPath == "" &&
		parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == raw && pathpkg.Clean(raw) == raw
}

type tokenMaterial struct {
	Version      int    `json:"version"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	CSRFSecret   []byte `json:"csrf_secret"`
}

func (s *Service) sealTokenMaterial(sessionHash Digest, material tokenMaterial) (Envelope, error) {
	if !validTokenMaterial(material) {
		return Envelope{}, ErrInvalidInput
	}
	plaintext, err := json.Marshal(material)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrEncryption, err)
	}
	return s.keys.Seal(sessionTokenPurpose, sessionHash, plaintext)
}

func (s *Service) openTokenMaterial(session SessionRecord) (tokenMaterial, error) {
	plaintext, err := s.keys.Open(sessionTokenPurpose, session.SessionHash, session.Tokens)
	if err != nil {
		return tokenMaterial{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var material tokenMaterial
	if err := decoder.Decode(&material); err != nil {
		return tokenMaterial{}, fmt.Errorf("%w: %w", ErrEncryption, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return tokenMaterial{}, ErrEncryption
	}
	if !validTokenMaterial(material) {
		return tokenMaterial{}, ErrEncryption
	}
	return material, nil
}

func validTokenMaterial(material tokenMaterial) bool {
	return material.Version == tokenMaterialVersion && material.AccessToken != "" && material.RefreshToken != "" &&
		material.IDToken != "" && len(material.CSRFSecret) == opaqueTokenBytes
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
