package consumer

import (
	"encoding/base32"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/pquerna/otp/totp"
)

func Test_validatePassword(t *testing.T) {
	type args struct {
		password string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"regular_password", args{"12345678"}, false},
		{"s dollar", args{"MY$SuperPassword11"}, true},
		{"asterisk", args{"MY*SuperPassword11"}, true},
		{"plus", args{"MY+SuperPassword11"}, true},
		{"minus", args{"MY-SuperPassword11"}, true},
		{"=", args{"MY=SuperPassword11"}, true},
		{"<", args{"MY>SuperPassword11"}, true},
		{"&", args{"MY&SuperPassword11"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validatePassword(tt.args.password); got != tt.want {
				t.Errorf("validatePassword() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_parseTime(t *testing.T) {
	got := ebs_fields.EbsDate()
	if len(got) != 12 {
		t.Fatalf("parseTime() length = %d, want 12", len(got))
	}
	for _, c := range got {
		if c < '0' || c > '9' {
			t.Fatalf("parseTime() contains non-digit: %q", c)
		}
	}
}

func Test_generateOtp(t *testing.T) {

	tests := []struct {
		name    string
		secret  string
		want    string
		wantErr bool
	}{
		{"test generateOtp", "12345678", "", false},
	}
	for _, tt := range tests {

		sec := base32.StdEncoding.EncodeToString([]byte(tt.secret))
		t.Run(tt.name, func(t *testing.T) {
			got, err := generateOtp(sec)
			if (err != nil) != tt.wantErr {
				t.Errorf("generateOtp() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !totp.Validate(got, sec) {
				t.Error("generateOtp() error not valid otp")
			}
		})
	}
}
