package tenantaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/google/uuid"
	"net"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type AuthorityAccount struct {
	Disabled        bool
	Subject         string
	Member          bool
	Roles           []tenantauth.Role
	Email, Fullname string
}
type Authority interface {
	Members(context.Context, string) ([]AuthorityAccount, error)
	Account(context.Context, string, string) (AuthorityAccount, error)
	SubjectExists(context.Context, string) (bool, error)
	ApplyDelta(context.Context, string, string, []tenantauth.Role, []tenantauth.Role) error
}
type Operation struct {
	Audit
	Issuer      string
	PayloadHash string
}
type Journal interface {
	WithTenant(context.Context, string, string, func(context.Context) error) error
	Operation(context.Context, accountenrollment.Identity, string) (*Operation, error)
	History(context.Context, accountenrollment.Identity) ([]Operation, error)
	// Create atomically persists the intent and an enrollment-complete receipt
	// whenever user is explicitly revoked, before any external authority write.
	Create(context.Context, Operation) error
	RecordRecovery(context.Context, accountenrollment.Identity, string, RecoveryAttempt) error
	Complete(context.Context, accountenrollment.Identity, string, time.Time, []tenantauth.Role) error
}
type Config struct {
	Issuer     string
	Catalog    tenantcatalog.Catalog
	Enrollment accountenrollment.Store
	Journal    Journal
	Authority  Authority
	Clock      func() time.Time
}
type Service struct{ config Config }

func New(config Config) (*Service, error) {
	if config.Issuer == "" || len(config.Catalog.All()) == 0 || config.Enrollment == nil || config.Journal == nil || config.Authority == nil || config.Clock == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{config: config}, nil
}
func canonicalID(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil && id.String() == raw
}
func (s *Service) authorize(actor Actor, permissions ...tenantauth.Permission) error {
	if actor.Kind != "" || actor.Issuer != s.config.Issuer || !canonicalID(actor.Subject) || !slices.Contains(actor.Roles, tenantauth.RoleTenantAdmin) {
		return ErrForbidden
	}
	if _, err := s.config.Catalog.Require(actor.TenantID); err != nil {
		return ErrForbidden
	}
	for _, permission := range permissions {
		if slices.Contains(actor.Permissions, permission) {
			return nil
		}
	}
	return ErrForbidden
}
func (s *Service) identity(actor Actor, subject string) (accountenrollment.Identity, error) {
	if !canonicalID(subject) {
		return accountenrollment.Identity{}, ErrInvalidRequest
	}
	return accountenrollment.Identity{Issuer: s.config.Issuer, Subject: subject, TenantID: actor.TenantID}, nil
}
func (s *Service) List(ctx context.Context, actor Actor) ([]Account, error) {
	if err := s.authorize(actor, tenantauth.PermissionIdentityAccessRead); err != nil {
		return nil, err
	}
	members, err := s.config.Authority.Members(ctx, actor.TenantID)
	if err != nil {
		return nil, ErrUnavailable
	}
	result := make([]Account, 0, len(members))
	for _, member := range members {
		account, err := s.Inspect(ctx, actor, member.Subject)
		if err != nil {
			return nil, err
		}
		if account.Member {
			result = append(result, account)
		}
	}
	return result, nil
}
func (s *Service) Inspect(ctx context.Context, actor Actor, subject string) (Account, error) {
	if err := s.authorize(actor, tenantauth.PermissionIdentityAccessRead, tenantauth.PermissionIdentityAccessWrite); err != nil {
		return Account{}, err
	}
	identity, err := s.identity(actor, subject)
	if err != nil {
		return Account{}, err
	}
	var result Account
	err = s.config.Enrollment.WithIdentity(ctx, identity, func(progress accountenrollment.ProgressAccess) error {
		var err error
		result, err = s.inspectLocked(ctx, identity, progress)
		return err
	})
	return result, err
}
func (s *Service) inspectLocked(ctx context.Context, id accountenrollment.Identity, progress accountenrollment.ProgressAccess) (Account, error) {
	ctx = accountenrollment.ProgressContext(ctx, progress)
	current, err := s.config.Authority.Account(ctx, id.TenantID, id.Subject)
	if err != nil {
		return Account{}, ErrUnavailable
	}
	if !validRoles(current.Roles) || (!current.Member && len(current.Roles) > 0) {
		return Account{}, ErrUnavailable
	}
	receipt, err := progress.Load(ctx)
	if err != nil {
		return Account{}, ErrUnavailable
	}
	history, err := s.config.Journal.History(ctx, id)
	if err != nil {
		return Account{}, ErrUnavailable
	}
	account := Account{TenantID: id.TenantID, Subject: id.Subject, Member: current.Member, Roles: append([]tenantauth.Role{}, current.Roles...), Permissions: tenantauth.PermissionsForRoles(current.Roles), EnrollmentSuppressed: receipt != nil && receipt.Complete}
	if current.Member {
		account.Disabled = current.Disabled
		account.Email = current.Email
		account.Fullname = current.Fullname
	}
	for _, op := range history {
		if op.Status == "pending" {
			if account.PendingOperationID != "" {
				return Account{}, ErrUnavailable
			}
			account.PendingOperationID = op.OperationID
		}
	}
	// The operation sequence participates even if an explicit no-op revoke only
	// changed durable enrollment suppression. History is ordered by creation/id.
	ids := make([]string, 0, len(history))
	for _, op := range history {
		ids = append(ids, op.OperationID+":"+op.Status)
	}
	payload, _ := json.Marshal(struct {
		Issuer     string
		Account    Account
		Operations []string
	}{id.Issuer, account, ids})
	digest := sha256.Sum256(payload)
	account.Revision = hex.EncodeToString(digest[:])
	return account, nil
}
func (s *Service) History(ctx context.Context, actor Actor, subject string) ([]Audit, error) {
	if err := s.authorize(actor, tenantauth.PermissionIdentityAccessRead, tenantauth.PermissionIdentityAccessWrite); err != nil {
		return nil, err
	}
	id, err := s.identity(actor, subject)
	if err != nil {
		return nil, err
	}
	var result []Audit
	err = s.config.Enrollment.WithIdentity(ctx, id, func(progress accountenrollment.ProgressAccess) error {
		ctx = accountenrollment.ProgressContext(ctx, progress)
		ops, err := s.config.Journal.History(ctx, id)
		if err != nil {
			return ErrUnavailable
		}
		result = make([]Audit, 0, len(ops))
		for _, op := range ops {
			result = append(result, op.Audit)
		}
		return nil
	})
	return result, err
}
func validRoles(roles []tenantauth.Role) bool {
	if !slices.IsSorted(roles) {
		return false
	}
	for i, role := range roles {
		if _, err := tenantauth.ParseTenantRole(string(role)); err != nil || i > 0 && roles[i-1] == role {
			return false
		}
	}
	return true
}
func validateChange(request ChangeRequest) error {
	if !canonicalID(request.OperationID) || !canonicalID(request.TargetSubject) || len(request.ExpectedRevision) != 64 || request.GrantRoles == nil || request.RevokeRoles == nil || !validRoles(request.GrantRoles) || !validRoles(request.RevokeRoles) || len(request.GrantRoles)+len(request.RevokeRoles) == 0 {
		return ErrInvalidRequest
	}
	digest, err := hex.DecodeString(request.ExpectedRevision)
	if err != nil || hex.EncodeToString(digest) != request.ExpectedRevision {
		return ErrInvalidRequest
	}
	for _, role := range request.GrantRoles {
		if slices.Contains(request.RevokeRoles, role) {
			return ErrInvalidRequest
		}
	}
	if request.Reason == "" || len(request.Reason) > 2000 || !utf8.ValidString(request.Reason) || strings.TrimSpace(request.Reason) != request.Reason || strings.IndexFunc(request.Reason, unicode.IsControl) >= 0 {
		return ErrInvalidRequest
	}
	return nil
}
func changedRoles(before, grant, revoke []tenantauth.Role) []tenantauth.Role {
	after := make([]tenantauth.Role, 0, 3)
	for _, role := range before {
		if !slices.Contains(revoke, role) {
			after = append(after, role)
		}
	}
	for _, role := range grant {
		if !slices.Contains(after, role) {
			after = append(after, role)
		}
	}
	slices.Sort(after)
	return after
}
func (s *Service) Change(ctx context.Context, actor Actor, request ChangeRequest) (ChangeResult, error) {
	if err := s.authorize(actor, tenantauth.PermissionIdentityAccessWrite); err != nil {
		return ChangeResult{}, err
	}
	if net.ParseIP(actor.SourceIP) == nil || actor.RequestID == "" || len(actor.RequestID) > 512 || strings.IndexFunc(actor.RequestID, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return ChangeResult{}, ErrInvalidRequest
	}
	return s.changeAuthorized(ctx, actor, request, false)
}
func (s *Service) changeAuthorized(ctx context.Context, actor Actor, request ChangeRequest, tenantLocked bool) (ChangeResult, error) {
	if err := validateChange(request); err != nil {
		return ChangeResult{}, err
	}
	id, err := s.identity(actor, request.TargetSubject)
	if err != nil {
		return ChangeResult{}, err
	}
	encoded, _ := json.Marshal(struct {
		Issuer, Tenant string
		Request        ChangeRequest
	}{id.Issuer, id.TenantID, request})
	digest := sha256.Sum256(encoded)
	payloadHash := hex.EncodeToString(digest[:])
	var result ChangeResult
	work := func(ctx context.Context) error {
		return s.config.Enrollment.WithIdentity(ctx, id, func(progress accountenrollment.ProgressAccess) error {
			ctx = accountenrollment.ProgressContext(ctx, progress)
			existing, err := s.config.Journal.Operation(ctx, id, request.OperationID)
			if err != nil {
				return ErrUnavailable
			}
			if existing != nil && existing.PayloadHash != payloadHash {
				return ErrOperationConflict
			}
			current, err := s.inspectLocked(ctx, id, progress)
			if err != nil {
				return err
			}
			if existing == nil {
				if current.PendingOperationID != "" {
					return ErrPendingOperation
				}
				if current.Revision != request.ExpectedRevision {
					return ErrRevisionConflict
				}
				exists, err := s.config.Authority.SubjectExists(ctx, id.Subject)
				if err != nil {
					return ErrUnavailable
				}
				if !exists {
					return ErrNotFound
				}
				if err := s.requireRemainingAdministrator(ctx, id, current.Roles, request.RevokeRoles); err != nil {
					return err
				}

				kind := "operator"
				if actor.Kind != "" {
					kind = actor.Kind
				}
				op := Operation{Issuer: id.Issuer, PayloadHash: payloadHash, Audit: Audit{ActorKind: kind, OperationID: request.OperationID, TenantID: id.TenantID, Subject: id.Subject, ActorSubject: actor.Subject, ActorRoles: slices.Clone(actor.Roles), ActorSourceIP: actor.SourceIP, ActorRequestID: actor.RequestID, RecoveryAttempts: []RecoveryAttempt{}, Reason: request.Reason, GrantRoles: slices.Clone(request.GrantRoles), RevokeRoles: slices.Clone(request.RevokeRoles), BeforeRoles: slices.Clone(current.Roles), AfterRoles: changedRoles(current.Roles, request.GrantRoles, request.RevokeRoles), ExpectedRevision: request.ExpectedRevision, Status: "pending", CreatedAt: s.config.Clock().UTC()}}
				if err = s.config.Journal.Create(ctx, op); err != nil {
					if errors.Is(err, ErrOperationConflict) {
						return err
					}
					return ErrUnavailable
				}
				existing = &op
			}
			if existing.Status == "complete" {
				result = ChangeResult{Account: current, Operation: existing.Audit}
				return nil
			}
			if existing.Status != "pending" || current.PendingOperationID != "" && current.PendingOperationID != existing.OperationID {
				return ErrPendingOperation
			}
			if existing.ActorRequestID != actor.RequestID || existing.ActorSubject != actor.Subject {
				attempt := RecoveryAttempt{ActorSubject: actor.Subject, ActorRoles: slices.Clone(actor.Roles), SourceIP: actor.SourceIP, RequestID: actor.RequestID, AttemptedAt: s.config.Clock().UTC()}
				if err = s.config.Journal.RecordRecovery(ctx, id, existing.OperationID, attempt); err != nil {
					return ErrUnavailable
				}
				reloaded, loadErr := s.config.Journal.Operation(ctx, id, request.OperationID)
				if loadErr != nil || reloaded == nil {
					return ErrUnavailable
				}
				existing = reloaded
			}
			if err := s.requireRemainingAdministrator(ctx, id, current.Roles, existing.RevokeRoles); err != nil {
				return err
			}
			// Save denial before the external removal, including a receipt-absent target
			// whose user role is already missing. Enrollment can never race a re-grant.
			if slices.Contains(existing.RevokeRoles, tenantauth.RoleUser) {
				if err = progress.Save(ctx, accountenrollment.Progress{Complete: true}); err != nil {
					return ErrUnavailable
				}
			}
			// Only the explicitly requested roles may change; retries never restore an
			// unrelated grant revoked by an operator through an external authority tool.
			if err = s.config.Authority.ApplyDelta(ctx, id.TenantID, id.Subject, existing.GrantRoles, existing.RevokeRoles); err != nil {
				return ErrUnavailable
			}
			verified, err := s.config.Authority.Account(ctx, id.TenantID, id.Subject)
			if err != nil || !validRoles(verified.Roles) || !requestedDeltaSatisfied(verified.Roles, existing.GrantRoles, existing.RevokeRoles) || (len(existing.GrantRoles) > 0 && !verified.Member) {
				return ErrUnavailable
			}
			completed := s.config.Clock().UTC()
			if err = s.config.Journal.Complete(ctx, id, existing.OperationID, completed, verified.Roles); err != nil {
				return ErrUnavailable
			}
			existing.Status = "complete"
			existing.CompletedAt = &completed
			existing.CompletedRoles = slices.Clone(verified.Roles)
			current, err = s.inspectLocked(ctx, id, progress)
			if err != nil {
				return err
			}
			result = ChangeResult{Account: current, Operation: existing.Audit}
			return nil
		})
	}
	if tenantLocked {
		err = work(ctx)
	} else {
		err = s.config.Journal.WithTenant(ctx, id.Issuer, id.TenantID, work)
	}
	return result, err
}
func requestedDeltaSatisfied(roles, grant, revoke []tenantauth.Role) bool {
	for _, role := range grant {
		if !slices.Contains(roles, role) {
			return false
		}
	}
	for _, role := range revoke {
		if slices.Contains(roles, role) {
			return false
		}
	}
	return true
}

func (s *Service) requireRemainingAdministrator(ctx context.Context, id accountenrollment.Identity, current, revoke []tenantauth.Role) error {
	if !slices.Contains(current, tenantauth.RoleTenantAdmin) || !slices.Contains(revoke, tenantauth.RoleTenantAdmin) {
		return nil
	}
	members, err := s.config.Authority.Members(ctx, id.TenantID)
	if err != nil {
		return ErrUnavailable
	}
	for _, member := range members {
		if member.Subject == id.Subject || !member.Member || member.Disabled || !slices.Contains(member.Roles, tenantauth.RoleTenantAdmin) {
			continue
		}
		other := id
		other.Subject = member.Subject
		history, err := s.config.Journal.History(ctx, other)
		if err != nil {
			return ErrUnavailable
		}
		pendingRemoval := false
		for _, op := range history {
			if op.Status == "pending" && slices.Contains(op.RevokeRoles, tenantauth.RoleTenantAdmin) {
				pendingRemoval = true
			}
		}
		if !pendingRemoval {
			return nil
		}
	}
	return ErrLastAdministrator
}
