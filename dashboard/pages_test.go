package dashboard

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDashboardTablePageRendersCoherentFilteredControls(t *testing.T) {
	view := DashboardTableView{
		PageCount:        2,
		CurrentPage:      2,
		TerminalIDFilter: "terminal-1",
		BasePath:         "/backoffice/t/tenant-a/reporting",
	}

	var output bytes.Buffer
	if err := DashboardTablePage(view).Render(context.Background(), &output); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	html := output.String()
	for _, want := range []string{
		`action="/backoffice/t/tenant-a/reporting"`,
		`name="tid" value="terminal-1"`,
		`href="/backoffice/t/tenant-a/reporting/stream?tid=terminal-1"`,
		`href="/backoffice/t/tenant-a/reporting?page=2&amp;tid=terminal-1"`,
		`class="page-item active"`,
		`Download matching data`,
		`Rejected or unknown`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard output missing %q: %s", want, html)
		}
	}
	for _, falseClaim := range []string{"Total Amount", "Top Transactions This Month", "Transaction Fees"} {
		if strings.Contains(html, falseClaim) {
			t.Fatalf("dashboard output contains unsupported aggregate %q", falseClaim)
		}
	}
}
