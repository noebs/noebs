package backofficeauth

import (
	"context"
	"net/http"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
)

const opaqueTokenBytes = 32

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// Digest is the one-way database identity of a browser-held opaque value.
type Digest [32]byte

// Envelope is an authenticated ciphertext. Key material and plaintext never
// cross the persistence interface.
type Envelope struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

type FlowRecord struct {
	StateHash    Digest
	BrowserHash  Digest
	PKCEVerifier Envelope
	NonceHash    Digest
	ReturnPath   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type SessionRecord struct {
	SessionHash       Digest
	Issuer            string
	Subject           string
	Tokens            Envelope
	AccessExpiresAt   time.Time
	RefreshExpiresAt  time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	LastSeenAt        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type SessionRefresh struct {
	Tokens           Envelope
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

type RefreshSessionFunc func(context.Context, SessionRecord) (SessionRefresh, error)

type FlowRepository interface {
	CreateFlow(context.Context, FlowRecord) error
	ConsumeFlow(context.Context, Digest, Digest, time.Time) (FlowRecord, error)
}

type SessionRepository interface {
	CreateSession(context.Context, SessionRecord) error
	LoadSession(context.Context, Digest) (SessionRecord, error)
	RefreshSession(context.Context, Digest, Clock, time.Duration, RefreshSessionFunc) (SessionRecord, error)
	// TouchSession atomically removes expired sessions and returns the current
	// record, whether this call advanced its idle deadline or another call did.
	TouchSession(context.Context, Digest, time.Time, time.Time, time.Time) (SessionRecord, error)
	DeleteSession(context.Context, Digest) (bool, error)
}

type LoginStart struct {
	AuthorizationURL string
	FlowCookie       *http.Cookie
}

type LoginComplete struct {
	SessionCookie   *http.Cookie
	ClearFlowCookie *http.Cookie
	ReturnPath      string
	Claims          tenantauth.Claims
}

type LogoutComplete struct {
	ClearSessionCookie *http.Cookie
	EndSessionURL      string
}

// AuthenticatedSession intentionally exposes no OAuth token material.
type AuthenticatedSession struct {
	Claims            tenantauth.Claims
	CSRFToken         string
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}
