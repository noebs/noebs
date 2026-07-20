package walletgrpc

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"google.golang.org/grpc/metadata"
)

type principalMetadataFixture struct {
	tenantID        string
	issuer          string
	subject         string
	organizationID  string
	authorizedParty string
	roles           string
	permission      string
	userID          string
	sourceIP        string
	expiresAt       time.Time
}

func userMetadata(userID int64, tenantID string) metadata.MD {
	return principalMetadataFixture{
		tenantID:        tenantID,
		issuer:          "https://api.noebs.sd/auth/realms/noebs",
		subject:         fmt.Sprintf("user-%d", userID),
		organizationID:  "org-" + tenantID,
		authorizedParty: "noebs-mobile",
		roles:           string(tenantauth.RoleUser),
		permission:      string(tenantauth.PermissionWalletRead),
		userID:          strconv.FormatInt(userID, 10),
		sourceIP:        "203.0.113.10",
		expiresAt:       time.Now().UTC().Add(5 * time.Minute),
	}.metadata()
}

func operatorMetadata(permission tenantauth.Permission) metadata.MD {
	return operatorMetadataForTenant("tenant", permission)
}

func operatorMetadataForTenant(tenantID string, permission tenantauth.Permission) metadata.MD {
	md := principalMetadataFixture{
		tenantID:        tenantID,
		issuer:          "https://api.noebs.sd/auth/realms/noebs",
		subject:         "backoffice-operator",
		organizationID:  "org-" + tenantID,
		authorizedParty: "noebs-backoffice",
		roles:           string(tenantauth.RoleTenantAdmin),
		permission:      string(permission),
		sourceIP:        "203.0.113.20",
		expiresAt:       time.Now().UTC().Add(5 * time.Minute),
	}.metadata()
	md.Set(backofficeauth.HeaderCSRFToken, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	return md
}

func adminMetadata() metadata.MD {
	return operatorMetadata(tenantauth.PermissionWalletRead)
}

func (f principalMetadataFixture) metadata() metadata.MD {
	return metadata.Pairs(
		gateway.GatewayTenantIDHeader, f.tenantID,
		gateway.GatewayIssuerHeader, f.issuer,
		gateway.GatewaySubjectHeader, f.subject,
		gateway.GatewayOrganizationIDHeader, f.organizationID,
		gateway.GatewayAuthorizedPartyHeader, f.authorizedParty,
		gateway.GatewayRolesHeader, f.roles,
		gateway.GatewayPermissionHeader, f.permission,
		gateway.GatewayUserIDHeader, f.userID,
		gateway.GatewaySourceIPHeader, f.sourceIP,
		gateway.GatewayTokenExpiresAtHeader, strconv.FormatInt(f.expiresAt.Unix(), 10),
	)
}

func setPrincipalMetadata(md metadata.MD, header, value string) metadata.MD {
	copyMD := md.Copy()
	copyMD.Set(header, value)
	return copyMD
}

func deletePrincipalMetadata(md metadata.MD, header string) metadata.MD {
	copyMD := md.Copy()
	copyMD.Delete(header)
	return copyMD
}

func mustPrincipal(t *testing.T, md metadata.MD) *gateway.PrincipalIdentity {
	t.Helper()
	principal, err := principalFromMetadata(md, time.Now().UTC())
	if err != nil {
		t.Fatalf("principalFromMetadata() error = %v", err)
	}
	if principal == nil {
		t.Fatal("principalFromMetadata() = nil")
	}
	return principal
}
