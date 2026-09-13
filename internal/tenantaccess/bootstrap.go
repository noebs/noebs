package tenantaccess

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/adonese/noebs/internal/accountenrollment"
	"net"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adonese/noebs/internal/pgsession"
	"github.com/adonese/noebs/internal/tenantauth"
)

const BootstrapEmail = "admin@noebs.sd"
const BootstrapUsername = "noebs-admin"

type BootstrapRequest struct {
	OperationID      string `json:"operation_id"`
	Reason           string `json:"reason"`
	ExpectedSubject  string `json:"expected_subject,omitempty"`
	BackofficeOrigin string `json:"backoffice_origin"`
}
type BootstrapReceipt struct {
	Request         BootstrapRequest `json:"request"`
	Issuer          string           `json:"issuer"`
	TenantID        string           `json:"tenant_id"`
	OperationID     string           `json:"operation_id"`
	Email           string           `json:"email"`
	Username        string           `json:"username"`
	Actor           Actor            `json:"actor"`
	Subject         string           `json:"subject,omitempty"`
	PayloadHash     string           `json:"payload_hash"`
	Change          *ChangeRequest   `json:"change,omitempty"`
	SetupDispatched bool             `json:"setup_dispatched"`
	Complete        bool             `json:"complete"`
}
type BootstrapJournal interface {
	AuthorizeBootstrap(context.Context) error
	BootstrapReceipt(context.Context, string, string) (*BootstrapReceipt, error)
	ReserveBootstrap(context.Context, BootstrapReceipt) error
	UpdateBootstrap(context.Context, BootstrapReceipt) error
}
type OperatorProvisioner interface {
	PrepareOperator(context.Context, string, string, string, string) (string, error)
	SendOperatorSetup(context.Context, string, string) error
}

// BootstrapOperator is a deployment-only, permanently one-time initialization.
// It reserves before identity/action writes, then uses the same role journal as
// normal access changes. Completed retries never reset credentials or send mail.
func (s *Service) BootstrapOperator(ctx context.Context, actor Actor, request BootstrapRequest, provisioner OperatorProvisioner) (ChangeResult, error) {
	journal, ok := s.config.Journal.(BootstrapJournal)
	if !ok {
		return ChangeResult{}, ErrForbidden
	}
	if err := journal.AuthorizeBootstrap(ctx); err != nil {
		return ChangeResult{}, err
	}
	if provisioner == nil || actor.Kind != "deployment-bootstrap" || actor.Issuer != s.config.Issuer || !canonicalID(actor.Subject) || len(actor.Roles) != 0 || len(actor.Permissions) != 0 || net.ParseIP(actor.SourceIP) == nil || actor.RequestID == "" || strings.IndexFunc(actor.RequestID, unicode.IsSpace) >= 0 || !canonicalID(request.OperationID) || request.Reason == "" || !utf8.ValidString(request.Reason) || len(request.Reason) > 2000 || strings.TrimSpace(request.Reason) != request.Reason || strings.IndexFunc(request.Reason, unicode.IsControl) >= 0 || (request.ExpectedSubject != "" && !canonicalID(request.ExpectedSubject)) || request.BackofficeOrigin == "" {
		return ChangeResult{}, ErrInvalidRequest
	}
	if _, err := s.config.Catalog.Require(actor.TenantID); err != nil {
		return ChangeResult{}, ErrForbidden
	}
	payload, _ := json.Marshal(request)
	digest := sha256.Sum256(payload)
	wanted := BootstrapReceipt{Request: request, Issuer: actor.Issuer, TenantID: actor.TenantID, OperationID: request.OperationID, Email: BootstrapEmail, Username: BootstrapUsername, Actor: actor, Subject: request.ExpectedSubject, PayloadHash: hex.EncodeToString(digest[:])}
	var result ChangeResult
	err := s.config.Journal.WithTenant(ctx, actor.Issuer, actor.TenantID, func(ctx context.Context) error {
		receipt, err := journal.BootstrapReceipt(ctx, actor.Issuer, actor.TenantID)
		if err != nil {
			return err
		}
		if receipt != nil {
			if receipt.Complete && (receipt.Change == nil || !receipt.SetupDispatched || !canonicalID(receipt.Subject)) {
				return ErrUnavailable
			}
			if receipt.OperationID != wanted.OperationID || receipt.PayloadHash != wanted.PayloadHash {
				return ErrOperationConflict
			}
		} else {
			members, err := s.config.Authority.Members(ctx, actor.TenantID)
			if err != nil {
				return ErrUnavailable
			}
			for _, member := range members {
				if slices.Contains(member.Roles, tenantauth.RoleTenantAdmin) {
					return ErrForbidden
				}
			}
			if err = journal.ReserveBootstrap(ctx, wanted); err != nil {
				return err
			}
			receipt = &wanted
		}
		if receipt.Change == nil {
			subject, err := provisioner.PrepareOperator(ctx, actor.TenantID, BootstrapEmail, request.ExpectedSubject, request.OperationID)
			if err != nil {
				return ErrUnavailable
			}
			if !canonicalID(subject) || (request.ExpectedSubject != "" && request.ExpectedSubject != subject) {
				return ErrUnavailable
			}
			// Read under the same identity lock signup uses. A previously active mobile
			// identity can never be converted by initial operator setup.
			inspector := actor
			inspector.Kind = ""
			inspector.Roles = []tenantauth.Role{tenantauth.RoleTenantAdmin}
			inspector.Permissions = []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}
			account, err := s.Inspect(ctx, inspector, subject)
			if err != nil {
				return err
			}
			if slices.Contains(account.Roles, tenantauth.RoleUser) {
				return ErrForbidden
			}
			receipt.Subject = subject
			receipt.Change = &ChangeRequest{OperationID: request.OperationID, TargetSubject: subject, GrantRoles: []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin}, RevokeRoles: []tenantauth.Role{tenantauth.RoleUser}, ExpectedRevision: account.Revision, Reason: request.Reason}
			if err = journal.UpdateBootstrap(ctx, *receipt); err != nil {
				return err
			}
		}
		if !receipt.SetupDispatched {
			if err = provisioner.SendOperatorSetup(ctx, receipt.Subject, request.BackofficeOrigin); err != nil {
				return ErrUnavailable
			}
			receipt.SetupDispatched = true
			if err = journal.UpdateBootstrap(ctx, *receipt); err != nil {
				return err
			}
		}
		// Actor identity in the original role audit is the reserved deployment actor;
		// the dedicated service-account identity is independently obtained from KC.
		changeActor := actor
		existing, err := s.config.Journal.Operation(ctx, accountenrollment.Identity{Issuer: actor.Issuer, TenantID: actor.TenantID, Subject: receipt.Subject}, receipt.OperationID)
		if err != nil {
			return ErrUnavailable
		}
		if existing == nil {
			changeActor = receipt.Actor
		}
		result, err = s.changeAuthorized(ctx, changeActor, *receipt.Change, true)
		if err != nil {
			return err
		}
		if !receipt.Complete {
			receipt.Complete = true
			return journal.UpdateBootstrap(ctx, *receipt)
		}
		return nil
	})
	return result, err
}
func (s *PostgresJournal) AuthorizeBootstrap(ctx context.Context) error {
	var allowed bool
	err := pgsession.Use(ctx, s.db).QueryRowContext(ctx, `SELECT current_database()='identity_auth' AND session_user='identity_auth_migrate' AND current_user='identity_auth_migrate' AND (SELECT pg_get_userbyid(datdba)='identity_auth_migrate' FROM pg_database WHERE datname=current_database())`).Scan(&allowed)
	if err != nil {
		return ErrUnavailable
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
func (s *PostgresJournal) BootstrapReceipt(ctx context.Context, issuer, tenant string) (*BootstrapReceipt, error) {
	if err := s.AuthorizeBootstrap(ctx); err != nil {
		return nil, err
	}
	var payload []byte
	err := pgsession.Use(ctx, s.db).QueryRowContext(ctx, `SELECT receipt FROM tenant_access_bootstrap WHERE issuer=$1 AND tenant_id=$2`, issuer, tenant).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result BootstrapReceipt
	if err = json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (s *PostgresJournal) ReserveBootstrap(ctx context.Context, receipt BootstrapReceipt) error {
	if err := s.AuthorizeBootstrap(ctx); err != nil {
		return err
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	_, err = pgsession.Use(ctx, s.db).ExecContext(ctx, `INSERT INTO tenant_access_bootstrap(issuer,tenant_id,receipt,created_at) VALUES($1,$2,$3::jsonb,$4)`, receipt.Issuer, receipt.TenantID, payload, time.Now().UTC())
	return err
}
func (s *PostgresJournal) UpdateBootstrap(ctx context.Context, receipt BootstrapReceipt) error {
	if err := s.AuthorizeBootstrap(ctx); err != nil {
		return err
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	result, err := pgsession.Use(ctx, s.db).ExecContext(ctx, `UPDATE tenant_access_bootstrap SET receipt=$4::jsonb WHERE issuer=$1 AND tenant_id=$2 AND receipt->>'operation_id'=$3 AND receipt->>'payload_hash'=$5 AND receipt->>'complete'='false'`, receipt.Issuer, receipt.TenantID, receipt.OperationID, payload, receipt.PayloadHash)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrOperationConflict
	}
	return nil
}
