// Package workloadauth authenticates HTTP requests exchanged between Noebs
// workloads. It signs the request itself, not a bearer credential that can be
// replayed against another method, target, body, audience, or identity context.
package workloadauth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"
)

const (
	VersionMagic = "NOEBS-WORKLOAD-V2"

	HeaderKeyID      = "X-Noebs-Workload-Key-ID"
	HeaderAudience   = "X-Noebs-Workload-Audience"
	HeaderTimestamp  = "X-Noebs-Workload-Timestamp"
	HeaderNonce      = "X-Noebs-Workload-Nonce"
	HeaderBodySHA256 = "X-Noebs-Workload-Body-SHA256"
	HeaderSignature  = "X-Noebs-Workload-Signature"

	HeaderRequestID       = "X-Request-ID"
	HeaderTenantID        = "X-Noebs-Tenant-ID"
	HeaderIssuer          = "X-Noebs-Issuer"
	HeaderSubject         = "X-Noebs-Subject"
	HeaderOrganizationID  = "X-Noebs-Organization-ID"
	HeaderAuthorizedParty = "X-Noebs-Authorized-Party"
	HeaderRoles           = "X-Noebs-Roles"
	HeaderPermission      = "X-Noebs-Permission"
	HeaderUserID          = "X-Noebs-User-ID"
	HeaderSourceIP        = "X-Noebs-Source-IP"
	HeaderTokenExpiresAt  = "X-Noebs-Token-Expires-At"
)

const (
	oldestAccepted = 30 * time.Second
	newestAccepted = 5 * time.Second
	nonceRetention = 90 * time.Second
)

var (
	ErrInvalidConfiguration = errors.New("invalid workload authentication configuration")
	ErrMissingSigner        = errors.New("missing workload request signer")
	ErrInvalidRequest       = errors.New("invalid signed request")
	ErrMissingHeader        = errors.New("missing signed header")
	ErrDuplicateHeader      = errors.New("duplicate signed header")
	ErrCredentialsPresent   = errors.New("workload credentials already present")
	ErrUnknownKey           = errors.New("unknown workload key")
	ErrAudienceMismatch     = errors.New("workload audience mismatch")
	ErrInvalidTimestamp     = errors.New("invalid workload timestamp")
	ErrTimestampExpired     = errors.New("workload timestamp expired")
	ErrTimestampInFuture    = errors.New("workload timestamp is in the future")
	ErrInvalidNonce         = errors.New("invalid workload nonce")
	ErrNonceSource          = errors.New("workload nonce source failure")
	ErrInvalidBodyDigest    = errors.New("invalid workload body digest")
	ErrBodyDigestMismatch   = errors.New("workload body digest mismatch")
	ErrInvalidSignature     = errors.New("invalid workload signature")
	ErrReplay               = errors.New("replayed workload request")
	ErrNonceStore           = errors.New("workload nonce store failure")
	ErrBodyRead             = errors.New("cannot read signed request body")
)

// Clock makes the accepted timestamp window explicit and testable.
type Clock interface {
	Now() time.Time
}

// NonceStore atomically records a nonce for a key and audience until expiresAt.
// Use returns true only for the first claim. Production implementations must
// be shared by every replica that accepts the same key ID for that audience.
type NonceStore interface {
	Use(ctx context.Context, keyID, audience, nonce string, expiresAt time.Time) (bool, error)
}

// RegisteredKey binds a public key to the workload identity that downstream
// authorization uses. Caller is never accepted from an HTTP header.
type RegisteredKey struct {
	Caller    string
	PublicKey ed25519.PublicKey
}

// Registry maps opaque, rotation-friendly key IDs to verified callers.
type Registry map[string]RegisteredKey

// Principal is returned only after the complete request and nonce have been
// verified. Identity headers remain on the request for the receiving boundary
// to validate and translate into domain values.
type Principal struct {
	Caller    string
	KeyID     string
	Audience  string
	RequestID string
	Nonce     string
	SignedAt  time.Time
}

var identityHeaders = [...]string{
	HeaderTenantID,
	HeaderIssuer,
	HeaderSubject,
	HeaderOrganizationID,
	HeaderAuthorizedParty,
	HeaderRoles,
	HeaderPermission,
	HeaderUserID,
	HeaderSourceIP,
	HeaderTokenExpiresAt,
}

var workloadHeaders = [...]string{
	HeaderKeyID,
	HeaderAudience,
	HeaderTimestamp,
	HeaderNonce,
	HeaderBodySHA256,
	HeaderSignature,
}

func WorkloadHeaderNames() []string {
	return append([]string(nil), workloadHeaders[:]...)
}

func IdentityHeaderNames() []string {
	return append([]string(nil), identityHeaders[:]...)
}
