package gateway

import (
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"
)

type UserModel struct {
	gorm.Model
	Username  string `binding:"required" json:"username" gorm:"unique_index"`
	Password  string `binding:"required,min=8,max=20" json:"password"`
	jwt       JWT
	jwtId     int
	Fullname  string `json:"fullname"`
	Birthday  string `json:"birthday"`
	Mobile    string `json:"mobile" binding:"required" gorm:"unique_index"`
	Email     string `json:"email"`
	Password2 string `binding:"required,eqfield=Password,min=8,max=20" json:"password2"`

	Card []Cards
}

type UserLogin struct {
	Username string `binding:"required" json:"username"`
	Password string `binding:"required" json:"password"`
}

func (m *UserModel) hashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(m.Password), 8)
	if err != nil {
		return err
	}
	m.Password = string(hashedPassword)
	m.Password2 = string(hashedPassword)
	return nil
}

type ErrorResponse struct {
	Code    uint
	Message string
}

type Cards struct {
	gorm.Model
	PAN       string `json:"pan" binding:"required"`
	Expdate   string `json:"exp_date" binding:"required"`
	IsPrimary bool   `json:"is_primary" binding:"required"`
}
