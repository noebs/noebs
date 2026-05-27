package walletgrpc

import (
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func adminMetadata() metadata.MD {
	return metadata.Pairs(strings.ToLower(gateway.GatewayAdminIdentityHeader), gateway.GatewayAdminIdentityValue)
}

func TestRequireAdmin(t *testing.T) {
	tests := []struct {
		name string
		cfg  ebs_fields.NoebsConfig
		md   metadata.MD
		want codes.Code
	}{
		{
			name: "gateway admin identity accepted",
			cfg:  ebs_fields.NoebsConfig{},
			md:   adminMetadata(),
			want: codes.OK,
		},
		{
			name: "missing gateway admin identity denied",
			cfg:  ebs_fields.NoebsConfig{},
			md:   metadata.MD{},
			want: codes.PermissionDenied,
		},
		{
			name: "invalid gateway admin identity denied",
			cfg:  ebs_fields.NoebsConfig{},
			md:   metadata.Pairs(strings.ToLower(gateway.GatewayAdminIdentityHeader), "wrong"),
			want: codes.PermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(&wallet.Service{
				Store:  &walletstore.Store{},
				Config: tt.cfg,
			})
			err := server.requireAdmin(tt.md)
			if status.Code(err) != tt.want {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), tt.want)
			}
		})
	}
}
