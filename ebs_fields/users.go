package ebs_fields

import (
	"encoding/base32"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// User contains User table in noebs. It should be kept simple and only contain the fields that are needed.
type User struct {
	Model
	TenantID string `json:"-"`
	Created  int64  `gorm:"autoCreateTime"`
	Password string `binding:"required,min=8,max=20" json:"password"`
	Fullname string `json:"fullname"`
	Username string `json:"username"`
	Gender   string `json:"gender"`
	Birthday string `json:"birthday"`

	Email         string `json:"email" gorm:"index"`
	Password2     string `json:"password2" gorm:"-"`
	IsMerchant    bool   `json:"is_merchant" gorm:"default:false"`
	PublicKey     string `json:"user_pubkey"`
	DeviceID      string `json:"device_id"`
	OTP           string `json:"otp"`
	SignedOTP     string `json:"signed_otp"`
	Tokens        []Token
	Beneficiaries []Beneficiary
	Cards         []Card
	// DeviceToken stores a push token (legacy db column is firebase_token).
	DeviceToken   string `json:"device_token" db:"firebase_token"`
	NewPassword   string `json:"new_password" gorm:"-"`
	IsPasswordOTP bool   `json:"is_password_otp" gorm:"default:false"`
	MainCard      string `json:"main_card" gorm:"column:main_card"`
	ExpDate       string `json:"exp_date" gorm:"column:main_expdate" db:"main_expdate"`
	Language      string `json:"language"`
	IsVerified    bool   `json:"is_verified"`
	Mobile        string `json:"mobile" gorm:"not null;uniqueIndex"`
	KYC           *KYC   `gorm:"foreignKey:UserMobile;references:Mobile"`
}

type KYC struct {
	Model
	TenantID    string   `json:"-"`
	UserMobile  string   `gorm:"not null;unique"`
	Mobile      string   `gorm:"primaryKey;not null;unique"`
	Passport    Passport `gorm:"foreignKey:Mobile;references:Mobile"`
	Selfie      string
	PassportImg string
}

type Passport struct {
	Model
	TenantID       string    `json:"-"`
	Mobile         string    `gorm:"primaryKey;not null;unique" json:"mobile,omitempty"`
	BirthDate      time.Time `json:"birth_date,omitempty"`
	IssueDate      time.Time `json:"issue_date,omitempty"`
	ExpirationDate time.Time `json:"expiration_date,omitempty"`
	NationalNumber string    `json:"national_number,omitempty"`
	PassportNumber string    `json:"passport_number,omitempty"`
	Gender         string    `json:"gender,omitempty"`
	Nationality    string    `json:"nationality,omitempty"`
	HolderName     string    `json:"holder_name,omitempty"`
}

type KYCPassport struct {
	Selfie      string `json:"selfie,omitempty"`
	PassportImg string `json:"passport_image,omitempty"`
	Passport
}

// UserProfile is a subset of the User struct, it contains information that appear in the user profile
// and which user can change.
type UserProfile struct {
	Fullname string `json:"fullname" binding:"required,min=1"`
	Username string `json:"username" binding:"min=1"`
	Email    string `json:"email" binding:"email"`
	Birthday string `json:"birthday"`
	Gender   string `json:"gender"`
}

// func (user *User) BeforeSave(tx *gorm.DB) (err error) {
// 	if user.Password == "" {
// 		return errors.New("password is empty")
// 	}
// 	if err := user.HashPassword(); err == nil {
// 		tx.Statement.SetColumn("password", user.Password)
// 		return nil
// 	} else {
// 		return err
// 	}
// }

type QRMerchant struct {
	Mobile string
}

// GenerateOtp for a noebs user
func (u User) GenerateOtp() (string, error) {
	if u.PublicKey == "" {
		return "", errors.New("no publickey")
	}
	code, err := totp.GenerateCodeCustom(u.EncodePublickey32(), time.Now(), totp.ValidateOpts{
		Period:    900,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})

	if err != nil {
		return "", err
	}
	return code, nil
}

// GenerateOTP for a noebs user
func (u User) VerifyOtp(code string) bool {
	if u.PublicKey == "" {
		return false
	}
	// using custom validator to increase OTP validation period
	isValid, _ := totp.ValidateCustom(
		code,
		u.EncodePublickey32(),
		time.Now().UTC(),
		totp.ValidateOpts{
			Period:    900,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		},
	)
	return isValid
}

// EncodePublickey a helper function to encode publickey since it has ---BEGIN and new lines
func (u User) EncodePublickey() string {
	return base64.StdEncoding.EncodeToString([]byte(u.PublicKey))
}

// EncodePublickey a helper function to encode publickey since it has ---BEGIN and new lines
func (u User) EncodePublickey32() string {
	return base32.StdEncoding.EncodeToString([]byte(u.PublicKey))
}

func (u *User) SanitizeName() {
	u.Mobile = strings.ToLower(u.Mobile)
}

func (u *User) HashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(u.Password), 8)
	if err != nil {
		return err
	}
	u.Password = string(hashedPassword)
	u.Password2 = string(hashedPassword)
	return nil
}

type Beneficiary struct {
	Model
	TenantID string `json:"-"`
	Data     string `json:"data"`
	BillType string `json:"bill_type"`
	UserID   int64
	Name     string `json:"name"` // a beneficiary name
}

func NewBeneficiary(number string, billType int, carrier, operator int) Beneficiary {
	var b Beneficiary
	b.Data = number
	switch billType {
	case 0: // it is a telecom
		if operator == 0 { // zain
			if carrier == 0 {
				b.BillType = "0010010001" // prepaid
			} else {
				b.BillType = "0010010002" // postpaid
			}
		} else if operator == 1 { // sudani
			if carrier == 0 {
				b.BillType = "0010010005" // prepaid
			} else {
				b.BillType = "0010010006" // postpaid
			}
		} else { // mtn
			if carrier == 0 {
				b.BillType = "0010010003" // prepaid
			} else {
				b.BillType = "0010010004" // postpaid
			}
		}
	case 1: // nec
		b.BillType = "0010020001"
	case 2: //p2p transfers
		b.BillType = "p2p"
	case 3: // E15
		b.BillType = "0010050001"
	case 4: // bashair
		b.BillType = "0010060002"
	case 5: // mohe Sudan FIXME: we're using the same label for sd and non-sd
		b.BillType = "0010030002"
	case 6: // customs
		b.BillType = "0010030003"
	case 7: // voucher
		b.BillType = "voucher"
	}
	return b
}

// Token a struct to represent a noebs payment order
// Noebs payment order is an abstraction layer built on top of EBS card transfer
// the idea is to allow noebs users to freely accept and transfer funds, without much of hassle
// that is needed when trying to register as a merchant. Any user can simply generate a payment token
// from noebs companioned apps and then proceed with payment. Another method is to generate a QR code
// which can be scanned by the other end to transfer money.
// A payment token includes the following information, more to come later:
//  1. UUID a unique UUID v4 per each operation, this is requested from ebs via [POST]/payment_token
//  2. ID a unique ID per each payment token, this is an optional field left for the user to supply. In e-commerce cases, an ID represent
//     the order ID.
//  3. Amount the amount to be transferred. Amount is required. A zero amount denotes a free payment.
//  4. UserID the user ID of the user who is making the payment. UserID is required.
//  5. Mobile: the receipient of the payment mobile. This is an optional field
//  6. Note: an optional text note to be sent to the recipient.
type Token struct {
	Model
	TenantID string `json:"-"`
	UserID   int64

	User         User          `json:"-"`
	Amount       int           `json:"amount,omitempty"`
	CartID       string        `json:"cart_id,omitempty"`
	UUID         string        `json:"uuid,omitempty"`
	Note         string        `json:"note,omitempty"`
	ToCard       string        `json:"toCard,omitempty"`
	EBSResponses []EBSResponse `json:"transaction,omitempty"`
	IsPaid       bool          `json:"is_paid"`
}

type QrData struct {
	UUID   string `json:"uuid"`
	ToCard string `json:"toCard,omitempty"`
	Amount int    `json:"amount,omitempty"`
}

// NewPaymentToken creates a new payment token and assign it to a user
func (u *User) NewPaymentToken(amount int, note string, cartID string) (*Token, error) {
	token := &Token{

		Amount: amount,
		Note:   note,
		CartID: cartID,
	}
	return token, nil
}

// Encode PaymentToken to a URL safe link that can be used for online purchases
func Encode(p *Token) (string, error) {
	var qr QrData
	qr.Amount = p.Amount
	qr.ToCard = p.ToCard
	qr.UUID = p.UUID
	data, err := json.Marshal(qr)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// Decode a noebs payment token to an internal PaymentToken that we understand
func Decode(data string) (Token, error) {
	var p Token
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(decoded, &p); err != nil {
		return p, err
	}
	return p, nil

}

// Card represents a single card in noebs.
type Card struct {
	Model
	TenantID string `json:"-"`
	Pan      string `json:"pan"`
	Expiry   string `json:"exp_date"`
	Name     string `json:"name"`
	IPIN     string `json:"ipin" gorm:"column:ipin"` // set gorm db name to ipin to avoid conflict with the field name in the struct
	UserID   int64
	IsMain   bool   `json:"is_main" gorm:"default:false"`
	CardIdx  string `json:"card_index" gorm:"-:all"`
	IsValid  *bool  `json:"is_valid"`
}

type CacheCards struct {
	Model
	TenantID  string `json:"-"`
	Pan       string `json:"pan" gorm:"uniqueIndex"`
	Expiry    string `json:"exp_date"`
	Name      string `json:"name"`
	Mobile    string `json:"mobile" gorm:"-:all"`
	Password  string `json:"password" gorm:"-:all"`
	PublicKey string `json:"user_pubkey" gorm:"-:all"`
	IsValid   *bool  `json:"is_valid"`
}

func (c CacheCards) OverrideField() string {
	return "is_valid"
}

func (c CacheCards) GetPk() string {
	return "pan"
}

func (c CacheCards) NewCardFromCached(id int) Card {
	return Card{
		Pan:    c.Pan,
		Expiry: c.Expiry,
		UserID: int64(id),
	}
}

// ExpandCard performs a regex search for the first and last 4 digits of a pan
// and retrieves the matching pan number
func ExpandCard(card string, userCards []Card) (string, error) {
	if len(card) < 8 {
		return "", errors.New("short query")
	}
	if userCards == nil {
		return "", errors.New("empty_cards")
	}

	// Create a list of strings
	var cards []string
	for _, v := range userCards {
		cards = append(cards, v.Pan)
	}
	search := card[:4] + ".*" + card[len(card)-4:] + "$"
	// Compile the regular expression pattern that matches the search string
	pattern := regexp.MustCompile(search)
	// Iterate through the list of strings
	for _, item := range cards {
		// Check if the list item matches the search string using the regular expression
		if pattern.MatchString(item) {
			return item, nil
		}
	}
	return "", errors.New("not able to find a match")
}
