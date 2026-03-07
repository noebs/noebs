package walletgrpc

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func adminContext(key string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-admin-key", key))
}

func basicAdminContext(user, pass string) context.Context {
	creds := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Basic "+creds))
}

func TestRequireAdmin(t *testing.T) {
	tests := []struct {
		name string
		cfg  ebs_fields.NoebsConfig
		md   metadata.MD
		want codes.Code
	}{
		{
			name: "debug bypass",
			cfg:  ebs_fields.NoebsConfig{IsDebug: true},
			md:   metadata.MD{},
			want: codes.OK,
		},
		{
			name: "admin key accepted",
			cfg:  ebs_fields.NoebsConfig{AdminKey: "secret"},
			md:   metadata.Pairs("x-admin-key", "secret"),
			want: codes.OK,
		},
		{
			name: "basic auth accepted",
			cfg:  ebs_fields.NoebsConfig{AdminUser: "admin", AdminPassword: "password"},
			md:   metadata.New(map[string]string{"authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:password"))}),
			want: codes.OK,
		},
		{
			name: "auth not configured",
			cfg:  ebs_fields.NoebsConfig{},
			md:   metadata.MD{},
			want: codes.Unavailable,
		},
		{
			name: "invalid key denied",
			cfg:  ebs_fields.NoebsConfig{AdminKey: "secret"},
			md:   metadata.Pairs("x-admin-key", "wrong"),
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
