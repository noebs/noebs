package walletgrpc

import (
	"strconv"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRequireAdmin(t *testing.T) {
	tests := []struct {
		name string
		md   metadata.MD
		want codes.Code
	}{
		{
			name: "backoffice role",
			md:   setPrincipalMetadata(adminMetadata(), gateway.GatewayRolesHeader, string(tenantauth.RoleBackoffice)),
			want: codes.OK,
		},
		{
			name: "tenant admin role",
			md:   setPrincipalMetadata(adminMetadata(), gateway.GatewayRolesHeader, string(tenantauth.RoleTenantAdmin)),
			want: codes.OK,
		},
		{
			name: "missing principal",
			md:   metadata.MD{},
			want: codes.PermissionDenied,
		},
		{
			name: "user role",
			md:   userMetadata(42, "tenant"),
			want: codes.PermissionDenied,
		},
		{
			name: "unknown role",
			md:   setPrincipalMetadata(adminMetadata(), gateway.GatewayRolesHeader, "admin"),
			want: codes.PermissionDenied,
		},
		{
			name: "incomplete principal",
			md:   deletePrincipalMetadata(adminMetadata(), gateway.GatewayIssuerHeader),
			want: codes.PermissionDenied,
		},
		{
			name: "expired principal",
			md: setPrincipalMetadata(
				adminMetadata(),
				gateway.GatewayTokenExpiresAtHeader,
				strconv.FormatInt(time.Now().UTC().Add(-time.Second).Unix(), 10),
			),
			want: codes.PermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(&wallet.Service{
				Store: &walletstore.Store{},
			})
			err := server.requireAdmin(tt.md)
			if status.Code(err) != tt.want {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), tt.want)
			}
		})
	}
}

func TestRequireAdminPermission(t *testing.T) {
	tests := []struct {
		name     string
		required tenantauth.Permission
		md       metadata.MD
		want     codes.Code
	}{
		{
			name:     "tenant admin with exact write permission",
			required: tenantauth.PermissionWalletFeesWrite,
			md:       operatorMetadata(tenantauth.PermissionWalletFeesWrite),
			want:     codes.OK,
		},
		{
			name:     "backoffice with exact read permission",
			required: tenantauth.PermissionWalletRead,
			md: setPrincipalMetadata(
				operatorMetadata(tenantauth.PermissionWalletRead),
				gateway.GatewayRolesHeader,
				string(tenantauth.RoleBackoffice),
			),
			want: codes.OK,
		},
		{
			name:     "backoffice cannot use write permission",
			required: tenantauth.PermissionWalletFeesWrite,
			md: setPrincipalMetadata(
				operatorMetadata(tenantauth.PermissionWalletFeesWrite),
				gateway.GatewayRolesHeader,
				string(tenantauth.RoleBackoffice),
			),
			want: codes.PermissionDenied,
		},
		{
			name:     "missing permission",
			required: tenantauth.PermissionWalletFeesWrite,
			md:       deletePrincipalMetadata(operatorMetadata(tenantauth.PermissionWalletFeesWrite), gateway.GatewayPermissionHeader),
			want:     codes.PermissionDenied,
		},
		{
			name:     "different permission",
			required: tenantauth.PermissionWalletFeesWrite,
			md:       operatorMetadata(tenantauth.PermissionWalletRead),
			want:     codes.PermissionDenied,
		},
		{
			name:     "user with exact permission",
			required: tenantauth.PermissionWalletFeesWrite,
			md: setPrincipalMetadata(
				userMetadata(42, "tenant"),
				gateway.GatewayPermissionHeader,
				string(tenantauth.PermissionWalletFeesWrite),
			),
			want: codes.PermissionDenied,
		},
	}

	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.requireAdminPermission(tt.md, tt.required)
			if status.Code(err) != tt.want {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), tt.want)
			}
		})
	}
}
