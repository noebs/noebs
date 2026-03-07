package walletgrpc

import (
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *Server) requireAdmin(md metadata.MD) error {
	if s == nil || s.Service == nil {
		return status.Error(codes.FailedPrecondition, "missing wallet service")
	}
	cfg := s.Service.Config
	if cfg.IsDebug {
		return nil
	}

	hasKey := strings.TrimSpace(cfg.AdminKey) != ""
	hasBasic := strings.TrimSpace(cfg.AdminUser) != "" && strings.TrimSpace(cfg.AdminPassword) != ""
	if !hasKey && !hasBasic {
		return status.Error(codes.Unavailable, "admin auth not configured")
	}

	if hasKey {
		for _, candidate := range md.Get("x-admin-key") {
			key := strings.TrimSpace(candidate)
			if key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(cfg.AdminKey)) == 1 {
				return nil
			}
		}
	}

	if hasBasic {
		for _, header := range md.Get("authorization") {
			if checkBasicAuth(header, cfg.AdminUser, cfg.AdminPassword) {
				return nil
			}
		}
	}

	return status.Error(codes.PermissionDenied, "unauthorized")
}

func checkBasicAuth(header, user, pass string) bool {
	if header == "" {
		return false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "basic" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return false
	}
	creds := strings.SplitN(string(decoded), ":", 2)
	if len(creds) != 2 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(creds[0]), []byte(user)) != 1 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(creds[1]), []byte(pass)) != 1 {
		return false
	}
	return true
}
