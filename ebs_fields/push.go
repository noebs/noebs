package ebs_fields

import "time"

// PushDataRecord stores notification data in the database.
type PushDataRecord struct {
	UUID           string    `json:"uuid"`
	TenantID       string    `json:"-"`
	Type           string    `json:"type"`
	Date           int64     `json:"date"`
	To             string    `json:"to"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	CallToAction   string    `json:"call_to_action"`
	Phone          string    `json:"phone"`
	IsRead         bool      `json:"is_read"`
	DeviceID       string    `json:"device_id"`
	UserMobile     string    `json:"user_mobile"`
	EBSUUID        string    `json:"-"`
	PaymentRequest QrData    `json:"payment_request"`
	CreatedAt      time.Time `json:"CreatedAt,omitempty"`
	UpdatedAt      time.Time `json:"UpdatedAt,omitempty"`
	DeletedAt      *time.Time

	EBSData EBSResponse `json:"data,omitempty"`
}
