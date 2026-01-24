package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/jmoiron/sqlx"
)

type MerchantTransactions struct {
	PurchaseAmount         float32 `json:"purchase_amount" db:"purchase_amount"`
	AllTransactions        int     `json:"purchases_count" db:"all_transactions"`
	SuccessfulTransactions int     `json:"successful_transactions" db:"successful_transactions"`
	FailedTransactions     int     `json:"failed_transactions" db:"failed_transactions"`
}

// To allow Redis to use this struct directly in marshaling
func (p *MerchantTransactions) MarshalBinary() ([]byte, error) {
	return json.Marshal(p)

}

// To allow Redis to use this struct directly in marshaling
func (p *MerchantTransactions) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, p)
}

func purchaseSum(tran []string) float32 {
	var trans []MerchantTransactions
	var mtran MerchantTransactions
	for _, k := range tran {
		json.Unmarshal([]byte(k), &mtran)
		trans = append(trans, mtran)
	}
	var sum float32
	for _, k := range trans {
		sum += k.PurchaseAmount
	}
	return sum
}

func ToPurchase(f ebs_fields.PurchaseFields) MerchantTransactions {
	amount := f.TranAmount
	var m MerchantTransactions
	m.PurchaseAmount = amount
	return m
}

type SearchModel struct {
	Page       int    `form:"page"`
	TerminalID string `form:"tid" binding:"required"`
}

func pagination(num int, page int) int {
	r := num % page
	if r == 0 {
		return num / page
	}
	return num/page + 1
}

func errorsCounter(t []ebs_fields.EBSResponse) int {
	var errors int
	for _, v := range t {
		if v.ResponseCode != 0 && v.ResponseStatus == "Successful" {
			errors++
		}
	}
	return errors
}

type dashboardStats struct {
	Amount float32
}

type merchantStats struct {
	//created_at, sum(tran_amount) as amount, terminal_id").Group("terminal_id"
	Amount     float32
	TerminalID string
}

func structToSlice(t []ebs_fields.EBSResponse) []string {
	var s []string
	for _, v := range t {
		d, _ := json.Marshal(v)
		s = append(s, string(d))
	}
	return s
}

func TimeFormatter(t time.Time) string {
	return t.Format(time.RFC3339)
}

type merchantsIssues struct {
	TerminalID string    `json:"terminalId" binding:"required" db:"terminal_id"`
	Latitude   float32   `json:"lat" db:"latitude"`
	Longitude  float32   `json:"long" db:"longitude"`
	Time       time.Time `json:"time" db:"reported_at"`
}

func (m *merchantsIssues) MarshalBinary() ([]byte, error) {
	return json.Marshal(m)
}

func sortTable(db *sqlx.DB, tenantID, searchField, search, sortField, sortCase string, offset, pageSize int) ([]ebs_fields.EBSResponse, int, error) {
	if db == nil {
		return nil, 0, fmt.Errorf("nil db")
	}

	searchField = normalizeSearchField(searchField)
	sortField = normalizeSortField(sortField)
	sortCase = normalizeSortCase(sortCase)
	log.Printf("the search field and sort fields are: %s, %s", searchField, sortField)

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
	return decodeTransactionRows(rows), count, nil
}

func normalizeSearchField(f string) string {
	f = mapSearchField(f)
	switch f {
	case "id", "terminal_id", "system_trace_audit_number", "approval_code", "created_at", "tran_date_time", "response_status", "uuid":
		return f
	default:
		return ""
	}
}

func normalizeSortField(f string) string {
	f = mapSearchField(f)
	switch f {
	case "id", "terminal_id", "system_trace_audit_number", "approval_code", "created_at", "tran_date_time", "tran_amount", "response_status", "response_code":
		return f
	default:
		return "id"
	}
}

func normalizeSortCase(sortCase string) string {
	switch strings.ToUpper(sortCase) {
	case "DESC":
		return "DESC"
	default:
		return "ASC"
	}
}

func mapSearchField(f string) string {
	/*
		terminalId: terminal_id
		tranDateTime: tran_date_time
		approvalCode: approval_code
	*/
	var result = f
	for i, v := range []rune(f) {
		if i == 0 {
			continue
		}
		if unicode.IsUpper(v) {
			if !unicode.IsUpper(rune(f[i-1])) {
				result = result[:i] + "_" + strings.ToLower(string(v)) + f[i+1:]
				break
			}

		}
	}
	return strings.ToLower(result)

}
