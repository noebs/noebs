package dashboard

import (
	"errors"
	"testing"
)

func Test_mapSearchField(t *testing.T) {

	tests := []struct {
		name string
		args string
		want string
	}{
		{"success_case", "terminalId", "terminal_id"},
		{"success_case_id_acronym", "terminalID", "terminal_id"},
		{"success_case", "approvalCode", "approval_code"},
		{"success_case_multi_word", "systemTraceAuditNumber", "system_trace_audit_number"},
		{"success_case_response_code", "responseCode", "response_code"},
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

func TestSortTableRejectsInvalidQueryFieldsBeforeDB(t *testing.T) {
	cases := []struct {
		name        string
		searchField string
		search      string
		sortField   string
		sortOrder   string
	}{
		{name: "search field", searchField: "unknownField", search: "value"},
		{name: "sort field", sortField: "unknownField"},
		{name: "sort order", sortOrder: "SIDEWAYS"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := sortTable(nil, "tenant-1", tt.searchField, tt.search, tt.sortField, tt.sortOrder, 0, 50)
			if !errors.Is(err, ErrInvalidDashboardQuery) {
				t.Fatalf("sortTable() error = %v, want %v", err, ErrInvalidDashboardQuery)
			}
		})
	}
}

func TestSortTableValidatesDashboardQueryBeforeDB(t *testing.T) {
	_, _, err := sortTable(nil, "tenant-1", "systemTraceAuditNumber", "123456", "responseCode", "DESC", 0, 50)
	if err == nil || err.Error() != "nil db" {
		t.Fatalf("sortTable() error = %v, want nil db after query validation", err)
	}
}
