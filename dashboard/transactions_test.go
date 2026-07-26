package dashboard

import (
	"errors"
	"net/url"
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

func TestPaginationUsesExactOneBasedPageCount(t *testing.T) {
	tests := []struct {
		records int
		want    int
	}{
		{records: 0, want: 0},
		{records: 1, want: 1},
		{records: 50, want: 1},
		{records: 51, want: 2},
		{records: 100, want: 2},
	}
	for _, test := range tests {
		if got := pagination(test.records, 50); got != test.want {
			t.Fatalf("pagination(%d, 50) = %d, want %d", test.records, got, test.want)
		}
	}
}

func TestDashboardURLsKeepTenantFilterAndOneBasedPage(t *testing.T) {
	view := DashboardTableView{
		BasePath:         dashboardBasePath("tenant-a"),
		TerminalIDFilter: "terminal 1",
	}

	pageURL, err := url.Parse(dashboardPageURL(view, 2))
	if err != nil {
		t.Fatalf("parse page URL: %v", err)
	}
	if pageURL.Path != "/backoffice/t/tenant-a/reporting" {
		t.Fatalf("page path = %q", pageURL.Path)
	}
	if pageURL.Query().Get("page") != "2" || pageURL.Query().Get("tid") != "terminal 1" {
		t.Fatalf("page query = %v", pageURL.Query())
	}

	streamURL, err := url.Parse(dashboardStreamURL(view))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if streamURL.Path != "/backoffice/t/tenant-a/reporting/stream" {
		t.Fatalf("stream path = %q", streamURL.Path)
	}
	if streamURL.Query().Get("tid") != "terminal 1" {
		t.Fatalf("stream query = %v", streamURL.Query())
	}
}

func TestDashboardTransactionFilterDoesNotInventTenantOrTerminal(t *testing.T) {
	where, args := dashboardTransactionFilter("tenant-a", "")
	if where != "tenant_id = ?" || len(args) != 1 || args[0] != "tenant-a" {
		t.Fatalf("unfiltered query = %q %#v", where, args)
	}

	where, args = dashboardTransactionFilter("tenant-a", "terminal-1")
	if where != "tenant_id = ? AND terminal_id LIKE ?" ||
		len(args) != 2 || args[0] != "tenant-a" || args[1] != "%terminal-1%" {
		t.Fatalf("filtered query = %q %#v", where, args)
	}
}
