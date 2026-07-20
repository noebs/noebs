package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"google.golang.org/grpc/metadata"
)

func setGatewayUserIdentityHeaders(req *http.Request, userID int64, tenantID, _ string) {
	setGatewayPrincipalTestHeaders(req.Header, tenantID, "user", "", userID)
}

func setGatewayAdminIdentityHeader(req *http.Request) {
	setGatewayPrincipalTestHeaders(
		req.Header,
		"test-tenant",
		"tenant-admin",
		tenantauth.PermissionWalletRead,
		0,
	)
	req.Header.Set(backofficeauth.HeaderCSRFToken, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
}

func setGatewayPrincipalTestHeaders(
	header http.Header,
	tenantID string,
	roles string,
	permission tenantauth.Permission,
	userID int64,
) {
	header.Set(gateway.GatewayTenantIDHeader, tenantID)
	header.Set(gateway.GatewayIssuerHeader, "https://identity.example/realms/noebs")
	header.Set(gateway.GatewaySubjectHeader, "subject-1")
	header.Set(gateway.GatewayOrganizationIDHeader, "org-"+tenantID)
	header.Set(gateway.GatewayAuthorizedPartyHeader, "test-client")
	header.Set(gateway.GatewayRolesHeader, roles)
	header.Set(gateway.GatewayPermissionHeader, string(permission))
	if userID > 0 {
		header.Set(gateway.GatewayUserIDHeader, strconv.FormatInt(userID, 10))
	}
	header.Set(gateway.GatewaySourceIPHeader, "203.0.113.10")
	header.Set(gateway.GatewayTokenExpiresAtHeader, strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
}

func gatewayUserIdentityContext(userID int64, tenantID, _ string) context.Context {
	header := make(http.Header)
	setGatewayPrincipalTestHeaders(header, tenantID, "user", "", userID)
	return gatewayMetadataContext(header)
}

func gatewayAdminIdentityContext() context.Context {
	header := make(http.Header)
	setGatewayPrincipalTestHeaders(
		header,
		"test-tenant",
		"tenant-admin",
		tenantauth.PermissionWalletRead,
		0,
	)
	return gatewayMetadataContext(header)
}

func gatewayMetadataContext(header http.Header) context.Context {
	values := make([]string, 0, 20)
	for _, name := range []string{
		gateway.GatewayTenantIDHeader,
		gateway.GatewayIssuerHeader,
		gateway.GatewaySubjectHeader,
		gateway.GatewayOrganizationIDHeader,
		gateway.GatewayAuthorizedPartyHeader,
		gateway.GatewayRolesHeader,
		gateway.GatewayPermissionHeader,
		gateway.GatewayUserIDHeader,
		gateway.GatewaySourceIPHeader,
		gateway.GatewayTokenExpiresAtHeader,
	} {
		values = append(values, strings.ToLower(name), header.Get(name))
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(values...))
}

func testGatewayPrincipalIdentity(tenantID, roles string, permission tenantauth.Permission, userID int64) gateway.PrincipalIdentity {
	header := make(http.Header)
	setGatewayPrincipalTestHeaders(header, tenantID, roles, permission, userID)
	principal, err := gateway.ParseInternalPrincipalIdentity(gateway.PrincipalHeaderValues{
		TenantID:        header.Get(gateway.GatewayTenantIDHeader),
		Issuer:          header.Get(gateway.GatewayIssuerHeader),
		Subject:         header.Get(gateway.GatewaySubjectHeader),
		OrganizationID:  header.Get(gateway.GatewayOrganizationIDHeader),
		AuthorizedParty: header.Get(gateway.GatewayAuthorizedPartyHeader),
		Roles:           header.Get(gateway.GatewayRolesHeader),
		Permission:      header.Get(gateway.GatewayPermissionHeader),
		UserID:          header.Get(gateway.GatewayUserIDHeader),
		SourceIP:        header.Get(gateway.GatewaySourceIPHeader),
		TokenExpiresAt:  header.Get(gateway.GatewayTokenExpiresAtHeader),
	}, time.Now())
	if err != nil {
		panic(err)
	}
	return principal
}
