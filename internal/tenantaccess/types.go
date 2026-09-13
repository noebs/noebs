// Package tenantaccess owns explicit, audited changes to tenant role grants.
// Keycloak remains the role authority; enrollment and access changes share a lock.
package tenantaccess

import (
	"context"
	"errors"
	"github.com/adonese/noebs/internal/tenantauth"
	"time"
)

var (
	ErrInvalidRequest    = errors.New("invalid tenant access request")
	ErrForbidden         = errors.New("tenant access denied")
	ErrNotFound          = errors.New("tenant account not found")
	ErrRevisionConflict  = errors.New("tenant access changed; review current roles")
	ErrOperationConflict = errors.New("operation ID was used with different terms")
	ErrPendingOperation  = errors.New("another tenant access change requires recovery")
	ErrLastAdministrator = errors.New("cannot remove the last tenant administrator")
	ErrUnavailable       = errors.New("tenant access authority unavailable")
)

type Actor struct {
	Kind        string
	Issuer      string
	Subject     string
	TenantID    string
	Roles       []tenantauth.Role
	Permissions []tenantauth.Permission
	SourceIP    string
	RequestID   string
}

type Account struct {
	Disabled             bool                    `json:"disabled"`
	TenantID             string                  `json:"tenant_id"`
	Subject              string                  `json:"subject"`
	Member               bool                    `json:"member"`
	Roles                []tenantauth.Role       `json:"roles"`
	Permissions          []tenantauth.Permission `json:"permissions"`
	Revision             string                  `json:"revision"`
	EnrollmentSuppressed bool                    `json:"enrollment_suppressed"`
	PendingOperationID   string                  `json:"pending_operation_id,omitempty"`
	Email                string                  `json:"email,omitempty"`
	Fullname             string                  `json:"fullname,omitempty"`
}

type ChangeRequest struct {
	OperationID      string            `json:"operation_id"`
	TargetSubject    string            `json:"target_subject"`
	GrantRoles       []tenantauth.Role `json:"grant_roles"`
	RevokeRoles      []tenantauth.Role `json:"revoke_roles"`
	ExpectedRevision string            `json:"expected_revision"`
	Reason           string            `json:"reason"`
}

type Audit struct {
	ActorKind        string            `json:"actor_kind"`
	CompletedRoles   []tenantauth.Role `json:"completed_roles"`
	OperationID      string            `json:"operation_id"`
	TenantID         string            `json:"tenant_id"`
	Subject          string            `json:"subject"`
	ActorSubject     string            `json:"actor_subject"`
	ActorRoles       []tenantauth.Role `json:"actor_roles"`
	ActorSourceIP    string            `json:"actor_source_ip"`
	ActorRequestID   string            `json:"actor_request_id"`
	RecoveryAttempts []RecoveryAttempt `json:"recovery_attempts"`
	Reason           string            `json:"reason"`
	GrantRoles       []tenantauth.Role `json:"grant_roles"`
	RevokeRoles      []tenantauth.Role `json:"revoke_roles"`
	BeforeRoles      []tenantauth.Role `json:"before_roles"`
	AfterRoles       []tenantauth.Role `json:"after_roles"`
	ExpectedRevision string            `json:"expected_revision"`
	Status           string            `json:"status"`
	CreatedAt        time.Time         `json:"created_at"`
	CompletedAt      *time.Time        `json:"completed_at,omitempty"`
}

type RecoveryAttempt struct {
	ActorSubject string            `json:"actor_subject"`
	ActorRoles   []tenantauth.Role `json:"actor_roles"`
	SourceIP     string            `json:"source_ip"`
	RequestID    string            `json:"request_id"`
	AttemptedAt  time.Time         `json:"attempted_at"`
}

type ChangeResult struct {
	Account   Account `json:"account"`
	Operation Audit   `json:"operation"`
}

// API is shared by the authenticated UI/API and operator client. Grant/revoke
// arrays are explicit deltas; the caller never replaces an entire tenant set.
type API interface {
	List(context.Context, Actor) ([]Account, error)
	Inspect(context.Context, Actor, string) (Account, error)
	Change(context.Context, Actor, ChangeRequest) (ChangeResult, error)
	History(context.Context, Actor, string) ([]Audit, error)
}
