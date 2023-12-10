package gateway

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestDbBackup_RemoteBackup(t *testing.T) {
	type fields struct {
		LastBackup time.Time
		Db         *gorm.DB
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{"test backup", fields{time.Now(), nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DbBackup{
				LastBackup: tt.fields.LastBackup,
				Db:         tt.fields.Db,
			}
			d.RemoteBackup()
		})
	}
}
