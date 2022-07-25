package gateway

import (
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

//UserModel contains User table in noebs. It should be kept simple and only contain the fields that are needed.
type UserModel struct {
	gorm.Model
	Username   string `json:"username" gorm:"index:idx_username,unique"`
	Password   string `binding:"required,min=8,max=20" json:"password"`
	Fullname   string `json:"fullname"`
	Birthday   string `json:"birthday"`
	Mobile     string `json:"mobile" binding:"required,len=10" gorm:"index:idx_mobile,unique"`
	Email      string `json:"email"`
	Password2  string `json:"password2" gorm:"-"`
	IsMerchant bool   `json:"is_merchant" gorm:"default:false"`
	PublicKey  string `json:"user_pubkey"`
	DeviceID   string `json:"device_id"`
}

//Token used by noebs client to refresh an existing token, that is Token.JWT
// Signature is the signed Message (username), and Message is just the username
type Token struct {
	JWT       string `json:"authorization"`
	Signature string `json:"signature"`
	Message   string `json:"message"`
}

func (u *UserModel) SanitizeName() {
	u.Username = strings.ToLower(u.Username)
}

func (u *UserModel) HashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(u.Password), 8)
	if err != nil {
		return err
	}
	u.Password = string(hashedPassword)
	u.Password2 = string(hashedPassword)
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
