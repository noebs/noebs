package wallet_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplicationOwnedWalletCredentialsStayRetired(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	repoRoot := filepath.Dir(filepath.Dir(sourceFile))

	removedFiles := []string{
		"wallet/activity/security.go",
		"wallet/security/pin.go",
		"wallet/security/totp.go",
		"wallet/store/user_2fa.go",
		"wallet/store/user_2fa_types.go",
		"store/migrations/postgres/wallet_ledger/013_wallet_user_2fa.sql",
	}
	for _, name := range removedFiles {
		_, err := os.Stat(filepath.Join(repoRoot, name))
		if err == nil {
			t.Errorf("retired credential file exists: %s", name)
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %s: %v", name, err)
		}
	}

	fragments := []string{
		"WalletPIN",
		"WalletPin",
		"wallet_pin",
		"Wallet2FAThreshold",
		"wallet_2fa_threshold",
		"UserTwoFA",
		"User2FA",
		"user_2fa",
		"TwoFACode",
		"TwoFaCode",
		"two_fa_code",
		"RequirePIN",
		"Require2FA",
		"VerifyWalletPIN",
		"VerifyUserTOTP",
		"pquerna/otp",
	}
	targets := []string{
		"wallet",
		"proto/noebs/wallet/v1/wallet.proto",
		"gen/proto/noebs/wallet/v1",
		"gen/openapi/noebs.swagger.json",
		"store/migrations/postgres/wallet_ledger",
		"cli/app_config.go",
		"cli/grpc_server.go",
		"ebs_fields/fields.go",
		"config.docker.yaml",
		"deploy/kubernetes/base/configmap.yaml",
		"go.mod",
		"go.sum",
	}

	for _, target := range targets {
		target := filepath.Join(repoRoot, target)
		err := filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || path == sourceFile {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(contents)
			for _, fragment := range fragments {
				if strings.Contains(text, fragment) {
					relative, relErr := filepath.Rel(repoRoot, path)
					if relErr != nil {
						return relErr
					}
					t.Errorf("retired wallet credential fragment %q in %s", fragment, relative)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", target, err)
		}
	}
}
