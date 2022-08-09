package gateway

import (
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestUserModel_sanitizeName(t *testing.T) {
	want := "mohamed"
	have := "MOHAMED"
	tests := []struct {
		name string
		want string
		have string
	}{
		{"testing lower - capital", want, have},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &ebs_fields.User{
				Mobile: have,
			}
			u.SanitizeName()
			if u.Mobile != want {
				t.Errorf("Want: %v, Have: %v", want, u.Mobile)
			}
		})
	}
}
