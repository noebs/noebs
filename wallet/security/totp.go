package security

import "github.com/pquerna/otp/totp"

func VerifyTOTP(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}
	return totp.Validate(code, secret)
}
