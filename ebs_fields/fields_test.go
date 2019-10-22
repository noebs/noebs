package ebs_fields

import "testing"

func Test_isEBS(t *testing.T) {

	tests := []struct {
		name string
		args string
		want bool
	}{
		// failed tests
		{"63918658585", "63918658585", true},
		{"failed tests", "858563918658585", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEBS(tt.args); got != tt.want {
				t.Errorf("isEBS() = %v, want %v", got, tt.want)
			}
		})
	}
}
