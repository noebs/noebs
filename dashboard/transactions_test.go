package dashboard

import "testing"

func Test_mapSearchField(t *testing.T) {

	tests := []struct {
		name string
		args string
		want string
	}{
		{"success_case", "terminalId", "terminal_id"},
		{"success_case", "approvalCode", "approval_code"},
		{"success_case", "approval_code", "approval_code"},
		{"created at test", "CreatedAt", "created_at"},
		{"test case for id", "ID", "id"},

	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapSearchField(tt.args); got != tt.want {
				t.Errorf("mapSearchField() = %v, want %v", got, tt.want)
			}
		})
	}
}
