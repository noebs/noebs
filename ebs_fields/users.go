package ebs_fields

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

type KYC struct {
	Model
	TenantID    string `json:"-"`
	UserID      int64  `json:"-"`
	Passport    Passport
	Selfie      string
	PassportImg string
}

type Passport struct {
	Model
	TenantID       string    `json:"-"`
	UserID         int64     `json:"-"`
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

type QRMerchant struct {
	Mobile string
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
//  3. Amount the amount to be transferred. A zero amount creates an open-amount token;
//     the payer must supply a positive amount when paying.
//  4. UserID the user ID of the user who is making the payment. UserID is required.
//  5. Mobile: the receipient of the payment mobile. This is an optional field
//  6. Note: an optional text note to be sent to the recipient.
type Token struct {
	Model    `json:"-"`
	TenantID string `json:"-"`
	UserID   int64  `json:"-"`

	Amount        int           `json:"amount,omitempty"`
	CartID        string        `json:"cart_id,omitempty"`
	UUID          string        `json:"uuid,omitempty"`
	Note          string        `json:"note,omitempty"`
	ToCard        string        `json:"toCard,omitempty"`
	ToCardEnc     string        `json:"-" db:"to_card_enc"`
	EBSResponses  []EBSResponse `json:"transaction,omitempty"`
	IsPaid        bool          `json:"is_paid"`
	PaymentStatus string        `json:"payment_status"`
	RailUUID      string        `json:"-"`
	PayerUserID   *int64        `json:"-"`
	ClaimedAmount *int          `json:"-"`
	ProcessingAt  *time.Time    `json:"-"`
	FinalizedAt   *time.Time    `json:"-"`
}

const (
	PaymentTokenStatusAvailable  = "available"
	PaymentTokenStatusProcessing = "processing"
	PaymentTokenStatusPaid       = "paid"
	PaymentTokenStatusFailed     = "failed"
)

type QrData struct {
	UUID   string `json:"uuid"`
	ToCard string `json:"toCard,omitempty"`
	Amount int    `json:"amount,omitempty"`
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
	PanEnc   string `json:"-" db:"pan_enc"`
	Expiry   string `json:"exp_date"`
	Name     string `json:"name"`
	IPIN     string `json:"ipin"`
	IPINEnc  string `json:"-" db:"ipin_enc"`
	UserID   int64
	Mobile   string `json:"mobile,omitempty"`
	IsMain   bool   `json:"is_main"`
	CardIdx  string `json:"card_index" db:"-"`
	IsValid  *bool  `json:"is_valid"`
}

// CardSummary is the complete public representation of an enrolled card.
// It exposes only the opaque card ID and display metadata; it contains no
// private database key, full PAN, fingerprint, ciphertext, mobile number,
// PIN, or IPIN.
type CardSummary struct {
	CardID    string `json:"card_id"`
	Name      string `json:"name"`
	MaskedPAN string `json:"masked_pan"`
	Expiry    string `json:"exp_date"`
	IsMain    bool   `json:"is_main"`
	Status    string `json:"status"`
}

var (
	ErrInvalidCardQuery   = errors.New("invalid card query")
	ErrEmptyCards         = errors.New("empty_cards")
	ErrCardQueryNotFound  = errors.New("card query not found")
	ErrAmbiguousCardQuery = errors.New("ambiguous card query")
)

// ExpandCard resolves a masked or full PAN selector by matching the first and last 4 digits.
func ExpandCard(card string, userCards []Card) (string, error) {
	card = strings.TrimSpace(card)
	if len(card) < 8 {
		return "", ErrInvalidCardQuery
	}
	if len(userCards) == 0 {
		return "", ErrEmptyCards
	}

	prefix := card[:4]
	suffix := card[len(card)-4:]
	if !digitsOnly(prefix) || !digitsOnly(suffix) {
		return "", ErrInvalidCardQuery
	}

	var match string
	for _, userCard := range userCards {
		pan := strings.TrimSpace(userCard.Pan)
		if strings.HasPrefix(pan, prefix) && strings.HasSuffix(pan, suffix) {
			if match != "" && pan != match {
				return "", ErrAmbiguousCardQuery
			}
			match = pan
		}
	}
	if match == "" {
		return "", ErrCardQueryNotFound
	}
	return match, nil
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
