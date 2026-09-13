package main

import (
	"errors"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/keycloakadmin"
	"github.com/adonese/noebs/internal/tenantaccess"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/store"
)

var tenantAccessService tenantaccess.API

func newTenantAccessService(cfg ebs_fields.NoebsConfig, db *store.DB, catalog tenantcatalog.Catalog) (*tenantaccess.Service, *keycloakadmin.AccessAuthority, error) {
	if db == nil || db.DB == nil || !cfg.AccountEnrollment.Enabled || cfg.AccountEnrollment.Issuer != cfg.OIDC.Issuer {
		return nil, nil, tenantaccess.ErrInvalidRequest
	}
	progress, err := accountenrollment.NewPostgresStore(db.DB.DB)
	if err != nil {
		return nil, nil, err
	}
	journal, err := tenantaccess.NewPostgresJournal(db.DB.DB)
	if err != nil {
		return nil, nil, err
	}
	tlsConfig, err := keycloakClientTLSConfig(cfg.KeycloakCACertificate)
	if err != nil {
		return nil, nil, err
	}
	client := httpclient.New(httpclient.WithTimeout(10*time.Second), httpclient.WithTLSConfig(tlsConfig), httpclient.WithResponseHeaderTimeout(5*time.Second))
	authority, err := keycloakadmin.NewAccessAuthority(cfg.AccountEnrollment, catalog, client)
	if err != nil {
		return nil, nil, err
	}
	service, err := tenantaccess.New(tenantaccess.Config{Issuer: cfg.OIDC.Issuer, Catalog: catalog, Enrollment: progress, Journal: journal, Authority: authority, Clock: time.Now})
	return service, authority, err
}
func initTenantAccess(role serviceRole, cfg ebs_fields.NoebsConfig, db *store.DB, catalog tenantcatalog.Catalog) error {
	tenantAccessService = nil
	if role != serviceRoleIdentityAuth || !cfg.AccountEnrollment.Enabled {
		return nil
	}
	if accountEnrollmentService == nil {
		return errors.New("tenant access requires initialized shared account enrollment")
	}
	service, _, err := newTenantAccessService(cfg, db, catalog)
	if err != nil {
		return err
	}
	tenantAccessService = service
	return nil
}
