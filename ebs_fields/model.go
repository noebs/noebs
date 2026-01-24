package ebs_fields

import "time"

// Model mirrors the fields we relied on from gorm.Model without the ORM dependency.
// Field names are preserved to avoid changing JSON output.
type Model struct {
	ID        int64
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}
