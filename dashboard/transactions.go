package dashboard

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/jmoiron/sqlx"
)

type MerchantTransactionCounts struct {
	AllTransactions        int `json:"transactions_count" db:"all_transactions"`
	SuccessfulTransactions int `json:"successful_transactions" db:"successful_transactions"`
	FailedTransactions     int `json:"failed_transactions" db:"failed_transactions"`
}

func pagination(num int, page int) int {
	r := num % page
	if r == 0 {
		return num / page
	}
	return num/page + 1
}

func dashboardTransactionFilter(tenantID, terminalID string) (string, []any) {
	if terminalID == "" {
		return "tenant_id = ?", []any{tenantID}
	}
	return "tenant_id = ? AND terminal_id LIKE ?", []any{tenantID, "%" + terminalID + "%"}
}

func dashboardBasePath(tenantID string) string {
	return "/backoffice/t/" + url.PathEscape(tenantID) + "/reporting"
}

func dashboardPageURL(data DashboardTableView, page int) string {
	query := url.Values{"page": []string{strconv.Itoa(page)}}
	if data.TerminalIDFilter != "" {
		query.Set("tid", data.TerminalIDFilter)
	}
	return data.BasePath + "?" + query.Encode()
}

func dashboardStreamURL(data DashboardTableView) string {
	if data.TerminalIDFilter == "" {
		return data.BasePath + "/stream"
	}
	query := url.Values{"tid": []string{data.TerminalIDFilter}}
	return data.BasePath + "/stream?" + query.Encode()
}

func transactionCurrency(tran ebs_fields.EBSResponse) string {
	if tran.TranCurrencyCode != "" {
		return tran.TranCurrencyCode
	}
	if tran.TranCurrency != "" {
		return tran.TranCurrency
	}
	return "Not reported"
}

func TimeFormatter(t time.Time) string {
	return t.Format(time.RFC3339)
}

func sortTable(db *sqlx.DB, tenantID, searchField, search, sortField, sortCase string, offset, pageSize int) ([]ebs_fields.EBSResponse, int, error) {
	var err error
	searchField, err = normalizeSearchField(searchField, search != "")
	if err != nil {
		return nil, 0, err
	}
	sortField, err = normalizeSortField(sortField)
	if err != nil {
		return nil, 0, err
	}
	sortCase, err = normalizeSortCase(sortCase)
	if err != nil {
		return nil, 0, err
	}
	if db == nil {
		return nil, 0, fmt.Errorf("nil db")
	}

	where := "tenant_id = ?"
	args := []any{tenantID}

	if search != "" {
		if searchField == "" {
			searchField = "terminal_id"
		}
		switch searchField {
		case "created_at":
			where += " AND created_at LIKE ?"
			args = append(args, "%"+search+"%")
		case "system_trace_audit_number":
			where += " AND system_trace_audit_number = ?"
			args = append(args, search)
		default:
			where += " AND " + searchField + " LIKE ?"
			args = append(args, "%"+search+"%")
		}
	}

	countQuery := "SELECT COUNT(*) FROM transactions WHERE " + where
	var count int
	if err := db.Get(&count, db.Rebind(countQuery), args...); err != nil {
		return nil, 0, err
	}

	query := fmt.Sprintf("SELECT id, created_at, updated_at, payload FROM transactions WHERE %s ORDER BY %s %s LIMIT ? OFFSET ?", where, sortField, sortCase)
	args = append(args, pageSize, offset)

	rows := []transactionRow{}
	if err := db.Select(&rows, db.Rebind(query), args...); err != nil {
		return nil, 0, err
	}
	transactions, err := decodeTransactionRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return transactions, count, nil
}

func validateDashboardTransactionQuery(searchField, search, sortField, sortCase string) error {
	if _, err := normalizeSearchField(searchField, search != ""); err != nil {
		return err
	}
	if _, err := normalizeSortField(sortField); err != nil {
		return err
	}
	if _, err := normalizeSortCase(sortCase); err != nil {
		return err
	}
	return nil
}

func normalizeSearchField(f string, searchProvided bool) (string, error) {
	f = mapSearchField(f)
	if f == "" {
		if searchProvided {
			return "terminal_id", nil
		}
		return "", nil
	}
	switch f {
	case "id", "terminal_id", "system_trace_audit_number", "approval_code", "created_at", "tran_date_time", "response_status", "uuid":
		return f, nil
	default:
		return "", fmt.Errorf("%w: invalid search field", ErrInvalidDashboardQuery)
	}
}

func normalizeSortField(f string) (string, error) {
	f = mapSearchField(f)
	if f == "" {
		return "id", nil
	}
	switch f {
	case "id", "terminal_id", "system_trace_audit_number", "approval_code", "created_at", "tran_date_time", "tran_amount", "response_status", "response_code":
		return f, nil
	default:
		return "", fmt.Errorf("%w: invalid sort field", ErrInvalidDashboardQuery)
	}
}

func normalizeSortCase(sortCase string) (string, error) {
	sortCase = strings.TrimSpace(sortCase)
	switch strings.ToUpper(sortCase) {
	case "":
		return "ASC", nil
	case "ASC":
		return "ASC", nil
	case "DESC":
		return "DESC", nil
	default:
		return "", fmt.Errorf("%w: invalid sort order", ErrInvalidDashboardQuery)
	}
}

func mapSearchField(f string) string {
	f = strings.TrimSpace(f)
	if f == "" {
		return ""
	}
	var result strings.Builder
	runes := []rune(f)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 && shouldSplitWord(runes, i) {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(r))
			continue
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

func shouldSplitWord(runes []rune, index int) bool {
	previous := runes[index-1]
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}
	if !unicode.IsUpper(previous) || index+1 >= len(runes) {
		return false
	}
	return unicode.IsLower(runes[index+1])
}
