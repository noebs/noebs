package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/keycloakadmin"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

var accountEnrollmentService *accountenrollment.Service

const accountContextInternalPath = "/internal/identity-auth/account/context"
const accountEnrollmentInternalPath = "/internal/identity-auth/account/enrollment"

func validateAccountEnrollmentRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	p := cfg.AccountEnrollment
	if p.KeycloakClientSecret != "" && role != serviceRoleIdentityAuth {
		return errors.New("account enrollment credential belongs only to identity-auth")
	}
	if !p.Enabled {
		return nil
	}
	if role != serviceRoleAPIGateway && role != serviceRoleIdentityAuth {
		return errors.New("account enrollment is only supported by api-gateway and identity-auth")
	}
	if p.Issuer != cfg.OIDC.Issuer {
		return errors.New("account enrollment issuer must equal configured OIDC issuer")
	}
	return p.Validate(tenantcatalog.Catalog{}, false)
}

func initAccountEnrollment(role serviceRole, cfg ebs_fields.NoebsConfig, db *store.DB, catalog tenantcatalog.Catalog) error {
	accountEnrollmentService = nil
	if err := validateAccountEnrollmentRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if role != serviceRoleIdentityAuth || !cfg.AccountEnrollment.Enabled {
		return nil
	}
	if db == nil || db.DB == nil {
		return accountenrollment.ErrUnavailable
	}
	state, err := accountenrollment.NewPostgresStore(db.DB.DB)
	if err != nil {
		return err
	}
	tlsConfig, err := keycloakClientTLSConfig(cfg.KeycloakCACertificate)
	if err != nil {
		return err
	}
	client := httpclient.New(httpclient.WithTimeout(10*time.Second), httpclient.WithTLSConfig(tlsConfig), httpclient.WithResponseHeaderTimeout(5*time.Second))
	authority, err := keycloakadmin.NewEnrollmentAuthority(cfg.AccountEnrollment, catalog, client)
	if err != nil {
		return err
	}
	accountEnrollmentService, err = accountenrollment.New(cfg.AccountEnrollment, catalog, authority, state)
	return err
}

func registerAccountEnrollmentInternalRoutes(route *fiber.App) {
	if !noebsConfig.AccountEnrollment.Enabled {
		return
	}
	handler := func(enroll bool) fiber.Handler {
		return func(c *fiber.Ctx) error {
			if accountEnrollmentService == nil {
				return accountEnrollmentFailure(c, http.StatusServiceUnavailable, "enrollment_unavailable")
			}
			if c.Locals(workloadPrincipalLocal) == nil {
				return accountEnrollmentFailure(c, http.StatusUnauthorized, "authentication_failed")
			}
			if err := validateEnrollmentBody(c, enroll); err != nil {
				return accountEnrollmentFailure(c, http.StatusBadRequest, "invalid_enrollment_request")
			}
			for _, name := range []string{workloadauth.HeaderOrganizationID, workloadauth.HeaderRoles, workloadauth.HeaderPermission, workloadauth.HeaderUserID} {
				if c.Get(name) != "" {
					return accountEnrollmentFailure(c, http.StatusUnauthorized, "authentication_failed")
				}
			}
			party := c.Get(workloadauth.HeaderAuthorizedParty)
			expiry, err := strconv.ParseInt(c.Get(workloadauth.HeaderTokenExpiresAt), 10, 64)
			if err != nil || expiry <= time.Now().Unix() || (party != "noebs-mobile" && party != "noebs-web") || net.ParseIP(c.Get(workloadauth.HeaderSourceIP)) == nil {
				return accountEnrollmentFailure(c, http.StatusUnauthorized, "authentication_failed")
			}
			identity := accountenrollment.Identity{Issuer: c.Get(workloadauth.HeaderIssuer), Subject: c.Get(workloadauth.HeaderSubject), TenantID: c.Get(workloadauth.HeaderTenantID)}
			ctx, cancel := context.WithTimeout(c.UserContext(), 30*time.Second)
			defer cancel()
			result, err := accountEnrollmentService.Execute(ctx, identity, enroll)
			if errors.Is(err, accountenrollment.ErrInvalidTenant) {
				return accountEnrollmentFailure(c, http.StatusBadRequest, "invalid_tenant")
			}
			if errors.Is(err, accountenrollment.ErrNotEligible) {
				return accountEnrollmentFailure(c, http.StatusForbidden, "enrollment_not_allowed")
			}
			if errors.Is(err, accountenrollment.ErrInvalidIdentity) {
				return accountEnrollmentFailure(c, http.StatusUnauthorized, "authentication_failed")
			}
			if err != nil {
				return accountEnrollmentFailure(c, http.StatusServiceUnavailable, "enrollment_unavailable")
			}
			c.Set("Cache-Control", "no-store")
			return c.JSON(result)
		}
	}
	route.Get(accountContextInternalPath, handler(false))
	route.Post(accountEnrollmentInternalPath, handler(true))
}

func registerAccountEnrollmentGatewayRoutes(route *fiber.App, cfg ebs_fields.NoebsConfig) {
	if !cfg.AccountEnrollment.Enabled {
		return
	}
	handler := func(enroll bool) fiber.Handler {
		return func(c *fiber.Ctx) error {
			if err := validateEnrollmentBody(c, enroll); err != nil {
				return accountEnrollmentFailure(c, http.StatusBadRequest, "invalid_enrollment_request")
			}
			values := c.Request().Header.PeekAll("Authorization")
			if oidcVerifier == nil || len(values) != 1 {
				return accountEnrollmentFailure(c, http.StatusUnauthorized, "authentication_failed")
			}
			claims, err := oidcVerifier.VerifyBearer(c.UserContext(), string(values[0]))
			if err != nil {
				return accountEnrollmentFailure(c, http.StatusUnauthorized, "authentication_failed")
			}
			identity := claims.Identity()
			if identity.AuthorizedParty != "noebs-mobile" && identity.AuthorizedParty != "noebs-web" {
				return accountEnrollmentFailure(c, http.StatusForbidden, "authorization_denied")
			}
			source, err := gatewayRequestSource(c)
			if err != nil {
				return accountEnrollmentFailure(c, http.StatusBadRequest, "invalid_request_source")
			}
			tenants := c.Request().Header.PeekAll("X-Active-Tenant")
			if len(tenants) != 1 {
				return accountEnrollmentFailure(c, http.StatusBadRequest, "invalid_tenant")
			}
			tenantID := string(tenants[0])
			if _, err := runtimeTenantCatalog.Require(tenantID); err != nil {
				return accountEnrollmentFailure(c, http.StatusBadRequest, "invalid_tenant")
			}
			result, err := requestAccountEnrollment(c.UserContext(), identity, tenantID, c.Get(workloadauth.HeaderRequestID), source, enroll)
			if err != nil {
				var failure *accountEnrollmentHTTPError
				if errors.As(err, &failure) {
					return accountEnrollmentFailure(c, failure.Status, failure.Code)
				}
				return accountEnrollmentFailure(c, http.StatusServiceUnavailable, "enrollment_unavailable")
			}
			c.Set("Cache-Control", "no-store")
			return c.JSON(result)
		}
	}
	route.Get("/consumer/auth/context", clearGatewayIdentityHeaders, handler(false))
	route.Post("/consumer/auth/enrollment", clearGatewayIdentityHeaders, handler(true))
}

func validateEnrollmentBody(c *fiber.Ctx, enroll bool) error {
	if len(c.Queries()) != 0 {
		return errors.New("query parameters are not accepted")
	}
	body := bytes.TrimSpace(c.Body())
	if !enroll {
		if len(body) != 0 {
			return errors.New("GET body is not accepted")
		}
		return nil
	}
	if !bytes.Equal(body, []byte("{}")) {
		return errors.New("enrollment accepts only an empty JSON object")
	}
	return nil
}

type accountEnrollmentHTTPError struct {
	Status int
	Code   string
}

func (e *accountEnrollmentHTTPError) Error() string { return e.Code }
func accountEnrollmentFailure(c *fiber.Ctx, status int, code string) error {
	c.Set("Cache-Control", "no-store")
	return c.Status(status).JSON(fiber.Map{"code": code, "message": strings.ReplaceAll(code, "_", " ")})
}

func requestAccountEnrollment(ctx context.Context, identity tenantauth.Identity, tenantID, requestID, sourceIP string, enroll bool) (accountenrollment.Context, error) {
	var result accountenrollment.Context
	if _, err := runtimeTenantCatalog.Require(tenantID); err != nil {
		return result, accountenrollment.ErrInvalidTenant
	}
	if workloadSigners == nil || identity.Issuer != noebsConfig.AccountEnrollment.Issuer || identity.Subject == "" || identity.ExpiresAt.Before(time.Now()) || (identity.AuthorizedParty != "noebs-mobile" && identity.AuthorizedParty != "noebs-web") || net.ParseIP(sourceIP) == nil {
		return result, accountenrollment.ErrInvalidIdentity
	}
	endpoint, err := serviceDiscoveryEndpoint(noebsConfig, serviceRoleIdentityAuth)
	if err != nil {
		return result, err
	}
	path, method := accountContextInternalPath, http.MethodGet
	var body []byte
	if enroll {
		path, method, body = accountEnrollmentInternalPath, http.MethodPost, []byte("{}")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	if enroll {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set(workloadauth.HeaderRequestID, requestID)
	request.Header.Set(workloadauth.HeaderTenantID, tenantID)
	request.Header.Set(workloadauth.HeaderIssuer, identity.Issuer)
	request.Header.Set(workloadauth.HeaderSubject, identity.Subject)
	request.Header.Set(workloadauth.HeaderAuthorizedParty, identity.AuthorizedParty)
	request.Header.Set(workloadauth.HeaderSourceIP, sourceIP)
	request.Header.Set(workloadauth.HeaderTokenExpiresAt, strconv.FormatInt(identity.ExpiresAt.Unix(), 10))
	if err := workloadSigners.Sign(string(serviceRoleIdentityAuth), request, body); err != nil {
		return result, err
	}
	client := newInternalHTTPClient(httpclient.WithTimeout(35*time.Second), httpclient.WithResponseHeaderTimeout(33*time.Second))
	client.CheckRedirect = workloadauth.RejectRedirect
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&failure)
		if failure.Code == "" {
			failure.Code = "enrollment_unavailable"
		}
		return result, &accountEnrollmentHTTPError{Status: response.StatusCode, Code: failure.Code}
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("decode enrollment response: %w", err)
	}
	return result, nil
}
