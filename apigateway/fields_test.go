package gateway

import (
	"testing"
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
			u := &UserModel{
				Username: have,
			}
			u.SanitizeName()
			if u.Username != want {
				t.Errorf("Want: %v, Have: %v", want, u.Username)
			}
		})
	}
}
