package transactionauth

import (
	"context"
	"crypto/sha256"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/internal/tenantcatalog"
)

const opaqueTokenBytes = 32

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Digest [sha256.Size]byte

type Operation string

const (
	OperationWalletP2P        Operation = "wallet.p2p"
	OperationWalletWithdrawal Operation = "wallet.withdrawal"
)

func (o Operation) Valid() bool {
	return o == OperationWalletP2P || o == OperationWalletWithdrawal
}

type Binding struct {
	TenantID       string
	Issuer         string
	Subject        string
	Operation      Operation
	RequestDigest  Digest
	IdempotencyKey string
}

func (b Binding) Validate() error {
	if b.TenantID == "" {
		return ErrMissingTenantID
	}
	parsedTenant, err := tenantcatalog.ParseID(b.TenantID)
	if err != nil || string(parsedTenant) != b.TenantID {
		return ErrInvalidTenantID
	}
	if b.Issuer == "" {
		return ErrMissingIssuer
	}
	issuer, err := url.Parse(b.Issuer)
	if err != nil || len(b.Issuer) > 2048 || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" ||
		issuer.Fragment != "" || issuer.String() != b.Issuer {
		return ErrInvalidIssuer
	}
	if b.Subject == "" {
		return ErrMissingSubject
	}
	if len(b.Subject) > 512 || strings.TrimSpace(b.Subject) != b.Subject {
		return ErrInvalidSubject
	}
	if !b.Operation.Valid() {
		return ErrInvalidOperation
	}
	if b.RequestDigest == (Digest{}) {
		return ErrMissingRequestDigest
	}
	if b.IdempotencyKey == "" {
		return ErrMissingIdempotencyKey
	}
	if len(b.IdempotencyKey) > 256 || strings.TrimSpace(b.IdempotencyKey) != b.IdempotencyKey {
		return ErrInvalidIdempotencyKey
	}
	return nil
}

type Envelope struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

type IntentRecord struct {
	IntentHash         Digest
	BrowserStartHash   Digest
	Binding            Binding
	CreatedAt          time.Time
	ExpiresAt          time.Time
	AuthorizedAt       time.Time
	AuthenticationTime time.Time
}

type NewFlowRecord struct {
	StateHash    Digest
	BrowserHash  Digest
	PKCEVerifier Envelope
	NonceHash    Digest
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type FlowRecord struct {
	NewFlowRecord
	IntentHash Digest
	Binding    Binding
}

type Repository interface {
	CreateIntent(context.Context, IntentRecord) error
	StartFlow(context.Context, Digest, NewFlowRecord, time.Time) (FlowRecord, error)
	ConsumeFlow(context.Context, Digest, Digest, time.Time) (FlowRecord, error)
	AuthorizeIntent(context.Context, Digest, time.Time, time.Time, time.Time) error
	ClaimIntent(context.Context, Digest, Binding, time.Time) error
	DeleteExpired(context.Context, time.Time) (int64, error)
}

type Initiated struct {
	IntentToken       string
	BrowserStartToken string
	ExpiresAt         time.Time
}

type BrowserChallenge struct {
	AuthorizationURL string
	BrowserBinding   string
	ExpiresAt        time.Time
}

type Authorization struct {
	IntentHash   Digest
	AuthorizedAt time.Time
	ExpiresAt    time.Time
}

type VerifiedIdentity struct {
	Issuer             string
	Subject            string
	ACR                string
	AuthenticationTime time.Time
}

type CodeExchanger interface {
	AuthorizationURL(state, nonce, verifier string) (string, error)
	Exchange(context.Context, string, string, Digest) (VerifiedIdentity, error)
}
