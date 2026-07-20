package transactionauth

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	flowVerifierPurpose             = "wallet-authorization-pkce-verifier"
	oidcAuthenticationTimePrecision = time.Second
)

type ServiceConfig struct {
	Repository       Repository
	OAuth            CodeExchanger
	Keys             *Keyring
	Clock            Clock
	Entropy          io.Reader
	RequiredACR      string
	BrowserStartTTL  time.Duration
	FlowTTL          time.Duration
	AuthorizationTTL time.Duration
}

type Service struct {
	repository       Repository
	oauth            CodeExchanger
	keys             *Keyring
	clock            Clock
	entropy          io.Reader
	requiredACR      string
	browserStartTTL  time.Duration
	flowTTL          time.Duration
	authorizationTTL time.Duration
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Repository == nil || config.OAuth == nil || config.Keys == nil || config.Clock == nil || config.Entropy == nil ||
		config.RequiredACR == "" ||
		!wholeSecond(config.BrowserStartTTL) || !wholeSecond(config.FlowTTL) || !wholeSecond(config.AuthorizationTTL) ||
		config.FlowTTL > config.BrowserStartTTL {
		return nil, ErrInvalidConfiguration
	}
	return &Service{
		repository:       config.Repository,
		oauth:            config.OAuth,
		keys:             config.Keys,
		clock:            config.Clock,
		entropy:          config.Entropy,
		requiredACR:      config.RequiredACR,
		browserStartTTL:  config.BrowserStartTTL,
		flowTTL:          config.FlowTTL,
		authorizationTTL: config.AuthorizationTTL,
	}, nil
}

func wholeSecond(value time.Duration) bool {
	return value > 0 && value%time.Second == 0
}

func (s *Service) Begin(ctx context.Context, binding Binding) (Initiated, error) {
	if s == nil || ctx == nil {
		return Initiated{}, ErrInvalidInput
	}
	if err := binding.Validate(); err != nil {
		return Initiated{}, err
	}
	intentToken, err := generateOpaque(s.entropy)
	if err != nil {
		return Initiated{}, err
	}
	browserStartToken, err := generateOpaque(s.entropy)
	if err != nil {
		return Initiated{}, err
	}
	intentHash, _ := digestOpaque(intentToken)
	browserStartHash, _ := digestOpaque(browserStartToken)
	now := s.clock.Now().UTC()
	expiresAt := now.Add(s.browserStartTTL)
	intent := IntentRecord{
		IntentHash:       intentHash,
		BrowserStartHash: browserStartHash,
		Binding:          binding,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
	}
	if err := s.repository.CreateIntent(ctx, intent); err != nil {
		return Initiated{}, err
	}
	return Initiated{
		IntentToken:       intentToken,
		BrowserStartToken: browserStartToken,
		ExpiresAt:         expiresAt,
	}, nil
}

func (s *Service) StartBrowser(ctx context.Context, browserStartToken string) (BrowserChallenge, error) {
	if s == nil || ctx == nil {
		return BrowserChallenge{}, ErrInvalidInput
	}
	browserStartHash, err := digestOpaque(browserStartToken)
	if err != nil {
		return BrowserChallenge{}, ErrInvalidBrowserStart
	}
	state, err := generateOpaque(s.entropy)
	if err != nil {
		return BrowserChallenge{}, err
	}
	browserBinding, err := generateOpaque(s.entropy)
	if err != nil {
		return BrowserChallenge{}, err
	}
	nonce, err := generateOpaque(s.entropy)
	if err != nil {
		return BrowserChallenge{}, err
	}
	verifier, err := generateOpaque(s.entropy)
	if err != nil {
		return BrowserChallenge{}, err
	}
	stateHash, _ := digestOpaque(state)
	sealedVerifier, err := s.keys.Seal(flowVerifierPurpose, stateHash, []byte(verifier))
	if err != nil {
		return BrowserChallenge{}, err
	}
	now := s.clock.Now().UTC()
	expiresAt := now.Add(s.flowTTL)
	flow := NewFlowRecord{
		StateHash:    stateHash,
		BrowserHash:  digestString(browserBinding),
		PKCEVerifier: sealedVerifier,
		NonceHash:    digestString(nonce),
		CreatedAt:    now,
		ExpiresAt:    expiresAt,
	}
	storedFlow, err := s.repository.StartFlow(ctx, browserStartHash, flow, now)
	if err != nil {
		return BrowserChallenge{}, err
	}
	authorizationURL, err := s.oauth.AuthorizationURL(state, nonce, verifier)
	if err != nil {
		return BrowserChallenge{}, err
	}
	return BrowserChallenge{
		AuthorizationURL: authorizationURL,
		BrowserBinding:   browserBinding,
		ExpiresAt:        storedFlow.ExpiresAt,
	}, nil
}

func (s *Service) Complete(ctx context.Context, state, browserBinding, code string) (Authorization, error) {
	if s == nil || ctx == nil || code == "" {
		return Authorization{}, ErrInvalidInput
	}
	stateHash, err := digestOpaque(state)
	if err != nil {
		return Authorization{}, ErrInvalidFlow
	}
	browserHash, err := digestOpaque(browserBinding)
	if err != nil {
		return Authorization{}, ErrInvalidFlow
	}
	now := s.clock.Now().UTC()
	flow, err := s.repository.ConsumeFlow(ctx, stateHash, browserHash, now)
	if err != nil {
		return Authorization{}, err
	}
	verifierBytes, err := s.keys.Open(flowVerifierPurpose, stateHash, flow.PKCEVerifier)
	if err != nil {
		return Authorization{}, err
	}
	verifier := string(verifierBytes)
	if _, err := digestOpaque(verifier); err != nil {
		return Authorization{}, ErrInvalidFlow
	}
	identity, err := s.oauth.Exchange(ctx, code, verifier, flow.NonceHash)
	if err != nil {
		return Authorization{}, err
	}
	if identity.Issuer != flow.Binding.Issuer || identity.Subject != flow.Binding.Subject || identity.ACR != s.requiredACR ||
		identity.AuthenticationTime.IsZero() ||
		identity.AuthenticationTime.Before(flow.CreatedAt.Add(-oidcAuthenticationTimePrecision)) {
		return Authorization{}, ErrAuthorizationDenied
	}
	completedAt := s.clock.Now().UTC()
	expiresAt := completedAt.Add(s.authorizationTTL)
	if err := s.repository.AuthorizeIntent(
		ctx,
		flow.IntentHash,
		completedAt,
		identity.AuthenticationTime.UTC(),
		expiresAt,
	); err != nil {
		return Authorization{}, err
	}
	return Authorization{IntentHash: flow.IntentHash, AuthorizedAt: completedAt, ExpiresAt: expiresAt}, nil
}

func (s *Service) Claim(ctx context.Context, intentToken string, binding Binding) error {
	if s == nil || ctx == nil {
		return ErrInvalidInput
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	intentHash, err := digestOpaque(intentToken)
	if err != nil {
		return ErrAuthorizationDenied
	}
	err = s.repository.ClaimIntent(ctx, intentHash, binding, s.clock.Now().UTC())
	if errors.Is(err, ErrIntentNotFound) {
		return ErrAuthorizationDenied
	}
	return err
}

func (s *Service) DeleteExpired(ctx context.Context) (int64, error) {
	if s == nil || ctx == nil {
		return 0, ErrInvalidInput
	}
	return s.repository.DeleteExpired(ctx, s.clock.Now().UTC())
}
