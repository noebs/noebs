package consumer

import "testing"

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
