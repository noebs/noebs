package consumer

import "github.com/adonese/noebs/ebs_fields"

// sanitizeUser strips sensitive fields before returning user payloads in APIs.
func sanitizeUser(user ebs_fields.User) ebs_fields.User {
	user.Password = ""
	user.Password2 = ""
	user.PublicKey = ""
	user.OTP = ""
	user.SignedOTP = ""
	user.MainCard = ""
	user.ExpDate = ""
	user.DeviceID = ""
	user.DeviceToken = ""
	user.NewPassword = ""
	user.KYC = nil
	user.Cards = nil
	user.Tokens = nil
	user.Beneficiaries = nil
	return user
}
