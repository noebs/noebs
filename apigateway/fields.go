package gateway

import "github.com/adonese/noebs/ebs_fields"

// Token used by noebs client to refresh an existing token, that is Token.JWT
// Signature is the signed Message (it could be a mobile username, or totp code)
// and Message is the raw message (it could be a mobile username, or totp code)
type Token struct {
	JWT       string `json:"authorization"`
	Signature string `json:"signature"`
	Message   string `json:"message"`
	Mobile    string `json:"mobile" binding:"required,len=10"`
}

type ErrorResponse struct {
	Code    uint
	Message string
}

type Cards struct {
	ebs_fields.Model
	PAN       string `json:"pan" binding:"required"`
	Expdate   string `json:"exp_date" binding:"required"`
	IsPrimary bool   `json:"is_primary" binding:"required"`
}
