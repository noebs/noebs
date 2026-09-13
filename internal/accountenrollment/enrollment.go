// Package accountenrollment owns the one-time transition from a realm identity
// to explicitly approved tenant memberships. Keycloak remains the authority.
package accountenrollment

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"slices"
	"strings"

	"github.com/adonese/noebs/internal/tenantcatalog"
)

var (
	ErrInvalidConfig   = errors.New("invalid account enrollment configuration")
	ErrInvalidIdentity = errors.New("invalid account enrollment identity")
	ErrInvalidTenant   = errors.New("invalid account enrollment tenant")
	ErrNotEligible     = errors.New("account enrollment is not available for this identity")
	ErrUnavailable     = errors.New("account enrollment unavailable")
)

// RuntimeConfig is explicit deployment policy. Credentials are supplied only
// to identity-auth; neither a public response nor the gateway needs them.
type RuntimeConfig struct {
	Enabled               bool     `json:"enabled" yaml:"enabled"`
	Issuer                string   `json:"issuer" yaml:"issuer"`
	TenantIDs             []string `json:"tenant_ids" yaml:"tenant_ids"`
	AllowedSubjects       []string `json:"allowed_subjects" yaml:"allowed_subjects"`
	AllowedVerifiedEmails []string `json:"allowed_verified_emails" yaml:"allowed_verified_emails"`
	AllowVerifiedAccounts bool     `json:"allow_verified_accounts" yaml:"allow_verified_accounts"`
	KeycloakClientID      string   `json:"keycloak_client_id" yaml:"keycloak_client_id"`
	KeycloakBaseURL       string   `json:"keycloak_base_url" yaml:"keycloak_base_url"`
	KeycloakClientSecret  string   `json:"keycloak_client_secret" yaml:"keycloak_client_secret"`
}

func (p RuntimeConfig) Validate(catalog tenantcatalog.Catalog, credentials bool) error {
	if !p.Enabled {
		return nil
	}
	u, err := url.Parse(p.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "/realms/noebs" && u.Path != "/auth/realms/noebs") {
		return fmt.Errorf("%w: issuer must be the exact HTTPS noebs realm", ErrInvalidConfig)
	}
	if !credentials {
		return nil
	}
	base, err := url.Parse(p.KeycloakBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.RawPath != "" || (base.Path != "" && base.Path != "/auth") {
		return fmt.Errorf("%w: internal Keycloak HTTPS origin is required", ErrInvalidConfig)
	}
	if len(p.TenantIDs) == 0 {
		return fmt.Errorf("%w: explicit allowed tenants are required", ErrInvalidConfig)
	}
	seen := map[string]bool{}
	for _, id := range p.TenantIDs {
		if _, err := catalog.Require(id); err != nil || seen[id] {
			return fmt.Errorf("%w: tenant IDs must be unique catalog entries", ErrInvalidConfig)
		}
		seen[id] = true
	}
	if p.AllowVerifiedAccounts && (len(p.AllowedSubjects) != 0 || len(p.AllowedVerifiedEmails) != 0) {
		return fmt.Errorf("%w: public verified-account enrollment and identity allowlists are mutually exclusive", ErrInvalidConfig)
	}
	for _, subject := range p.AllowedSubjects {
		if subject == "" || strings.TrimSpace(subject) != subject {
			return fmt.Errorf("%w: empty or noncanonical allowed subject", ErrInvalidConfig)
		}
	}
	for _, email := range p.AllowedVerifiedEmails {
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email || strings.ToLower(email) != email {
			return fmt.Errorf("%w: allowed emails must be canonical lowercase addresses", ErrInvalidConfig)
		}
	}
	if p.KeycloakClientID != "noebs-account-enroller" || p.KeycloakClientSecret == "" {
		return fmt.Errorf("%w: dedicated enrollment credential is required", ErrInvalidConfig)
	}
	return nil
}

type Identity struct{ Issuer, Subject, TenantID string }
type Tenant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Context struct {
	Tenants           []Tenant `json:"tenants"`
	PreferredTenantID *string  `json:"preferred_tenant_id"`
	EnrollmentStatus  string   `json:"enrollment_status"`
	RefreshRequired   bool     `json:"refresh_required"`
	SuggestedFullname string   `json:"suggested_fullname,omitempty"`
}

type Account struct {
	BootstrapOperationID string
	Fullname             string
	Email                string
	EmailVerified        bool
	// Memberships includes existing classes even when they do not permit mobile use.
	Memberships map[string][]string
}

type Authority interface {
	Inspect(context.Context, string) (Account, error)
	EnsureUserMembership(context.Context, string, string) error
}

// A receipt is scoped to one caller-selected tenant. It must survive later
// membership revocation, so repeating signup cannot restore removed access.
type Progress struct {
	Complete bool `json:"complete"`
}
type ProgressAccess interface {
	Load(context.Context) (*Progress, error)
	Save(context.Context, Progress) error
}

// PendingAccess guards process-death recovery between external role mutations.
// Production progress stores must expose it; pure in-memory enrollment fixtures
// without access management have no pending journal to consult.
type PendingAccess interface {
	HasPendingAccessChange(context.Context) (bool, error)
}

type PendingBootstrapAccess interface {
	HasPendingBootstrapOperation(context.Context, string) (bool, error)
}

type Store interface {
	WithIdentity(context.Context, Identity, func(ProgressAccess) error) error
}

type Service struct {
	policy    RuntimeConfig
	catalog   tenantcatalog.Catalog
	authority Authority
	store     Store
}

func New(policy RuntimeConfig, catalog tenantcatalog.Catalog, authority Authority, store Store) (*Service, error) {
	if err := policy.Validate(catalog, true); err != nil {
		return nil, err
	}
	if !policy.Enabled || authority == nil || store == nil {
		return nil, ErrInvalidConfig
	}
	return &Service{policy: policy, catalog: catalog, authority: authority, store: store}, nil
}

func (s *Service) Execute(ctx context.Context, identity Identity, enroll bool) (Context, error) {
	result := Context{Tenants: []Tenant{}, EnrollmentStatus: "required"}
	if identity.Issuer != s.policy.Issuer || identity.Subject == "" || strings.TrimSpace(identity.Subject) != identity.Subject {
		return result, ErrInvalidIdentity
	}
	tenant, err := s.catalog.Require(identity.TenantID)
	if err != nil {
		return result, ErrInvalidTenant
	}
	err = s.store.WithIdentity(ctx, identity, func(access ProgressAccess) error {
		if pending, ok := access.(PendingAccess); ok {
			blocked, err := pending.HasPendingAccessChange(ctx)
			if err != nil || blocked {
				return ErrUnavailable
			}
		}
		progress, err := access.Load(ctx)
		if err != nil {
			return err
		}
		account, err := s.authority.Inspect(ctx, identity.Subject)
		if err != nil {
			return err
		}
		if account.BootstrapOperationID != "" {
			gate, ok := access.(PendingBootstrapAccess)
			if !ok {
				return ErrUnavailable
			}
			pending, err := gate.HasPendingBootstrapOperation(ctx, account.BootstrapOperationID)
			if err != nil || pending {
				return ErrUnavailable
			}
		}
		classes, member := account.Memberships[identity.TenantID]
		eligible := slices.Contains(s.policy.TenantIDs, identity.TenantID) && s.eligible(identity.Subject, account)
		if enroll && progress == nil {
			// Existing memberships can be adopted without granting or replacing access,
			// even while enrollment policy excludes this account or tenant.
			if !eligible && (!member || len(classes) == 0) {
				return ErrNotEligible
			}
			progress = &Progress{Complete: member && len(classes) > 0}
			if err := access.Save(ctx, *progress); err != nil {
				return err
			}
		}
		if enroll && progress != nil && !progress.Complete {
			if !eligible {
				return ErrNotEligible
			}
			if !member || len(classes) == 0 {
				if err := s.authority.EnsureUserMembership(ctx, identity.Subject, identity.TenantID); err != nil {
					return err
				}
			}
			progress.Complete = true
			if err := access.Save(ctx, *progress); err != nil {
				return err
			}
			account, err = s.authority.Inspect(ctx, identity.Subject)
			if err != nil {
				return err
			}
		}
		if progress != nil {
			result.EnrollmentStatus = "pending"
			if progress.Complete {
				result.EnrollmentStatus = "complete"
			}
		}
		if slices.Contains(account.Memberships[identity.TenantID], "user") {
			result.Tenants = append(result.Tenants, Tenant{ID: identity.TenantID, Name: tenant.Name})
			selected := identity.TenantID
			result.PreferredTenantID = &selected
		}
		result.SuggestedFullname = account.Fullname
		// Lost successful responses are safe: repeating POST does not grant again,
		// but the caller still refreshes tokens before using its selected tenant.
		result.RefreshRequired = enroll && len(result.Tenants) > 0
		return nil
	})
	return result, err
}

func (s *Service) eligible(subject string, account Account) bool {
	return slices.Contains(s.policy.AllowedSubjects, subject) || account.EmailVerified && account.Email != "" && (s.policy.AllowVerifiedAccounts || slices.Contains(s.policy.AllowedVerifiedEmails, strings.ToLower(account.Email)))
}
