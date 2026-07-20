package ebs_fields

import (
	"errors"
	"strings"
	"time"
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

type QrData struct {
	UUID   string `json:"uuid"`
	ToCard string `json:"toCard,omitempty"`
	Amount int    `json:"amount,omitempty"`
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
