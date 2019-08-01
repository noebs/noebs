package gateway

import (
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"
)

type UserModel struct {
	gorm.Model
	Username string `binding:"required" json:"username" gorm:"unique_index"`
	Password string `binding:"required" json:"password"`
	JWT      JWT
	JWTID    int
	Fullname string `json:"fullname"`
	Birthday string `json:"birthday"`
	Mobile   string `json:"mobile" binding:"required" gorm:"unique_index"`
}

func (m *UserModel) hashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(m.Password), 8)
	if err != nil {
		return err
	}
	m.Password = string(hashedPassword)
	return nil
}

type ErrorResponse struct {
	Code    uint
	Message string
}
