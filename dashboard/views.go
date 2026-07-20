package dashboard

import "github.com/adonese/noebs/ebs_fields"

type DashboardStatsView struct {
	NumberTransactions     int
	SuccessfulTransactions int
	FailedTransactions     int
}

type DashboardTableView struct {
	Transactions   []ebs_fields.EBSResponse
	PageCount      int
	Stats          DashboardStatsView
	Amounts        dashboardStats
	MerchantStats  []merchantStats
	LeastMerchants []merchantStats
	TerminalFees   []merchantStats
	SumFees        float32
}

type QRStatusView struct {
	Transactions []ebs_fields.EBSResponse
	PageCount    int
}
