package users

import "github.com/jinzhu/gorm"

type Client struct {
	gorm.Model

	Terminal []Terminal

	BankID     uint
	MerchantID uint
}

type Bank struct {
	gorm.Model

	Terminal []Terminal
	Merchant []Merchant

	Client Client
}

type Merchant struct {
	gorm.Model

	MobileNumber string
	Terminal     Terminal

	ClientID uint
	BankID   uint
}

type Terminal struct {
	gorm.Model
	Name           string
	TerminalNumber string `gorm:"unqiue_index"`

	BankID     uint
	MerchantID uint
	ClientID   uint
}

// `User` belongs to `Profile`, `ProfileID` is the foreign key

//type User struct {
//	gorm.Model
//	Profile   Profile
//	ProfileID int
//}
//
//type Profile struct {
//	gorm.Model
//	Name string
//}
//
//
//
//// User has one CreditCard, UserID is the foreign key
//type User struct {
//	gorm.Model
//	CreditCard   CreditCard
//}
//
//type CreditCard struct {
//	gorm.Model
//	UserID   uint
//	Number   string
//}
//
//
//
//// User has many emails, UserID is the foreign key
//type User struct {
//	gorm.Model
//	Emails   []Email
//}
//
//type Email struct {
//	gorm.Model
//	Email   string
//	UserID  uint
//}
