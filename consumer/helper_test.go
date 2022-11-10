package consumer

import (
	"testing"

	"github.com/adonese/noebs/ebs_fields"
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
	tests := []struct {
		name string
		want string
	}{
		{"parse time", "301112121212"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ebs_fields.EbsDate(); got != tt.want {
				t.Errorf("parseTime() = %v, want %v", got, tt.want)
			}
		})
	}
}