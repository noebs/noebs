package gateway

import (
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"
)

type UserModel struct {
	gorm.Model
	Username  string `binding:"required_without=Email" json:"username" gorm:"unique_index"`
	Password  string `binding:"required" json:"password"`
	JWT       JWT
	JWTID     int
	Fullname  string `json:"fullname"`
	Birthday  string `json:"birthday"`
	Mobile    string `json:"mobile" binding:"required" gorm:"unique_index"`
	Email     string `json:"email" binding:"email,required_without=Username"`
	Password2 string `binding:"required,eqfield=Password,min=8,max=20" json:"password2"`

	Card []Cards
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

type Cards struct {
	gorm.Model
	PAN     string `json:"pan"`
	Expdate string `json:"exp_date"`
}
