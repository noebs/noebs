package gateway

import "golang.org/x/crypto/bcrypt"

type Request struct {
	ServiceID string `binding:"required"`
	Password  string `binding:"required"`
}

func (r Request) HashPassword() []byte {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(r.Password), 8)
	if err != nil {
		// there's an unhandled error here.
		panic(err) // FIXME don't panic
	}
	return hashedPassword
}

type ErrorResponse struct {
	Code    uint
	Message string
}
