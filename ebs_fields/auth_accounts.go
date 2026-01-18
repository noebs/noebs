package ebs_fields

import "gorm.io/gorm"

// AuthAccount links a user to an external auth provider (e.g., Google).
type AuthAccount struct {
	gorm.Model
	UserID         uint   `json:"user_id" gorm:"index;not null"`
	Provider       string `json:"provider" gorm:"size:32;not null;index:idx_provider_subject,unique"`
	ProviderUserID string `json:"provider_user_id" gorm:"size:191;not null;index:idx_provider_subject,unique"`
	Email          string `json:"email,omitempty" gorm:"size:191;index"`
	EmailVerified  bool   `json:"email_verified"`
}
