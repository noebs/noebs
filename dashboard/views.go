package dashboard

import "github.com/adonese/noebs/ebs_fields"

type DashboardStatsView struct {
	NumberTransactions     int
	SuccessfulTransactions int
	FailedTransactions     int
}

type DashboardTableView struct {
	Transactions     []ebs_fields.EBSResponse
	PageCount        int
	CurrentPage      int
	Stats            DashboardStatsView
	TerminalIDFilter string
	BasePath         string
}

type QRStatusView struct {
	Transactions []ebs_fields.EBSResponse
}
