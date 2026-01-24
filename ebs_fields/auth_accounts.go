package ebs_fields

// AuthAccount links a user to an external auth provider (e.g., Google).
type AuthAccount struct {
	Model
	TenantID       string `json:"-"`
	UserID         int64  `json:"user_id"`
	Provider       string `json:"provider"`
	ProviderUserID string `json:"provider_user_id"`
	Email          string `json:"email,omitempty"`
	EmailVerified  bool   `json:"email_verified"`
}
