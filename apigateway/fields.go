package gateway

import (
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"
)

type UserModel struct {
	gorm.Model
	ServiceID string `binding:"required" json:"service_id"`
	Password  string `binding:"required" json:"password"`
	JWT       JWT
	JWTID     int
}

func (um *UserModel) hashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(um.Password), 8)
	if err != nil {
		return err
	}
	um.Password = string(hashedPassword)
	return nil
}

type ErrorResponse struct {
	Code    uint
	Message string
}
