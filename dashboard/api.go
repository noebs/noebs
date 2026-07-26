package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/parsing"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

var (
	ErrInvalidPagination     = errors.New("invalid pagination")
	ErrInvalidDashboardQuery = errors.New("invalid dashboard query")
)

type Service struct {
	Store       *store.Store
	NoebsConfig ebs_fields.NoebsConfig
}

func (s Service) calculateOffset(page, pageSize int) uint {
	if page == 0 {
		page++
	}
	return uint((page - 1) * pageSize)
}

func parsePositiveQueryInt(raw string, defaultValue int) (int, error) {
	value, err := parsing.PositiveIntOrDefaultParam(map[string]string{"value": raw}, "value", defaultValue)
	if err != nil {
		return 0, ErrInvalidPagination
	}
	return value, nil
}

func rejectInvalidPagination(c *fiber.Ctx, err error) {
	jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "invalid_pagination", "message": err.Error()})
}

func rejectInvalidDashboardQuery(c *fiber.Ctx, err error) {
	jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "invalid_query", "message": err.Error()})
}

func (s *Service) TransactionsCount(c *fiber.Ctx) {
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	var count int64
	stmt := db.Rebind("SELECT COUNT(*) FROM transactions WHERE tenant_id = ?")
	if err := db.GetContext(c.UserContext(), &count, stmt, tenantID); err != nil {
		log.WithFields(logrus.Fields{"code": err.Error(), "details": "error in database"}).Info("error in database")
		c.SendStatus(404)
		return
	}

	jsonResponse(c, http.StatusOK, fiber.Map{"result": count})
}

func (s *Service) TransactionByTid(c *fiber.Ctx) {
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	tid := c.Query("tid")
	if tid == "" {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "tid is required"})
		return
	}

	tran, err := fetchTransactions(
		c.UserContext(),
		db,
		`SELECT id, created_at, updated_at, payload FROM transactions
		 WHERE tenant_id = ? AND terminal_id LIKE ?
		 ORDER BY id DESC`,
		tenantID,
		tid+"%",
	)
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

	jsonResponse(c, http.StatusOK, fiber.Map{"result": tran, "count": len(tran)})
}

func (s *Service) GetAll(c *fiber.Ctx) {
	p := c.Query("page", "")
	size := c.Query("size", "50")
	perPage := c.Query("perPage", "")

	search := c.Query("search", "")
	searchField := c.Query("field", "")
	sortField := c.Query("sort_field", "")
	sortCase := c.Query("order", "")

	if perPage != "" {
		size = perPage
	}
	pageSize, err := parsePositiveQueryInt(size, 50)
	if err != nil {
		rejectInvalidPagination(c, err)
		return
	}
	page, err := parsePositiveQueryInt(p, 1)
	if err != nil {
		rejectInvalidPagination(c, err)
		return
	}
	if err := validateDashboardTransactionQuery(searchField, search, sortField, sortCase); err != nil {
		rejectInvalidDashboardQuery(c, err)
		return
	}

	offset := s.calculateOffset(page, pageSize)

	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	tran, count, err := sortTable(db, tenantID, searchField, search, sortField, sortCase, int(offset), pageSize)
	if err != nil {
		if errors.Is(err, ErrInvalidDashboardQuery) {
			rejectInvalidDashboardQuery(c, err)
			return
		}
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

	paging := map[string]int{
		"previous": page - 1,
		"after":    page + 1,
		"count":    count,
	}
	jsonResponse(c, http.StatusOK, fiber.Map{"result": tran, "paging": paging})
}

// GetID gets a transaction by its database ID.
func (s *Service) GetID(c *fiber.Ctx) {
	id := c.Params("id")
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	tran, err := fetchTransactions(
		c.UserContext(),
		db,
		"SELECT id, created_at, updated_at, payload FROM transactions WHERE tenant_id = ? AND id = ? LIMIT 1",
		tenantID,
		id,
	)
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	if len(tran) == 0 {
		c.SendStatus(404)
		return
	}
	jsonResponse(c, http.StatusOK, fiber.Map{"result": tran[0]})
}

func (s *Service) BrowserDashboard(c *fiber.Ctx) {
	page, err := parsePositiveQueryInt(c.Query("page", ""), 1)
	if err != nil {
		rejectInvalidPagination(c, err)
		return
	}

	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

	pageSize := 50
	offset := (page - 1) * pageSize
	terminalID := c.Query("tid")
	where, args := dashboardTransactionFilter(tenantID, terminalID)

	var statsRow dashboardTransactionStats
	statsQuery := `SELECT
		COUNT(*) AS number_transactions,
		COALESCE(SUM(CASE WHEN response_code = 0 THEN 1 ELSE 0 END), 0) AS successful_transactions,
		COALESCE(SUM(CASE WHEN response_code = 0 THEN 0 ELSE 1 END), 0) AS failed_transactions
		FROM transactions WHERE ` + where
	if err := db.GetContext(c.UserContext(), &statsRow, db.Rebind(statsQuery), args...); err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	stats := DashboardStatsView{
		NumberTransactions:     statsRow.NumberTransactions,
		SuccessfulTransactions: statsRow.SuccessfulTransactions,
		FailedTransactions:     statsRow.FailedTransactions,
	}

	rowsQuery := `SELECT id, created_at, updated_at, payload
		FROM transactions WHERE ` + where + `
		ORDER BY id DESC LIMIT ? OFFSET ?`
	rowArgs := append(append([]any{}, args...), pageSize, offset)
	tran, err := fetchTransactions(c.UserContext(), db, db.Rebind(rowsQuery), rowArgs...)
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

	view := DashboardTableView{
		Transactions:     tran,
		PageCount:        pagination(stats.NumberTransactions, pageSize),
		CurrentPage:      page,
		Stats:            stats,
		TerminalIDFilter: terminalID,
		BasePath:         dashboardBasePath(tenantID),
	}
	renderComponent(c, http.StatusOK, DashboardTablePage(view))
}

func (s *Service) QRStatus(c *fiber.Ctx) {

	q := c.Query("id")
	if q == "" {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "id is required"})
		return
	}
	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	data, err := s.getLastTransactions(c.UserContext(), tenantID, q)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error()})
		return
	}

	view := QRStatusView{
		Transactions: data,
	}
	renderComponent(c, http.StatusOK, QRStatusPage(view))
}

func (s *Service) getLastTransactions(ctx context.Context, tenantID, merchantID string) ([]ebs_fields.EBSResponse, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	return fetchTransactions(
		ctx,
		db,
		`SELECT id, created_at, updated_at, payload FROM transactions
		 WHERE tenant_id = ? AND merchant_id = ?
		 ORDER BY id DESC LIMIT 50`,
		tenantID,
		merchantID,
	)
}

func (s *Service) IndexPage(c *fiber.Ctx) {
	renderComponent(c, http.StatusOK, DashboardIndexPage())
}

func (s *Service) Stream(c *fiber.Ctx) {
	var stream bytes.Buffer

	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	where, args := dashboardTransactionFilter(tenantID, c.Query("tid"))
	trans, err := fetchTransactions(
		c.UserContext(),
		db,
		db.Rebind("SELECT id, created_at, updated_at, payload FROM transactions WHERE "+where+" ORDER BY id DESC"),
		args...,
	)
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	if err := json.NewEncoder(&stream).Encode(trans); err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

	c.Set("Content-Disposition", `attachment; filename="transactions.json"`)
	c.Set("Content-Type", fiber.MIMEApplicationJSONCharsetUTF8)
	if err := c.SendStream(&stream); err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

}

func (s *Service) MerchantTransactionsEndpoint(c *fiber.Ctx) {
	tid := c.Query("terminal")
	if tid == "" {
		// the user didn't sent any id
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "terminal id not present in url params",
			"code": "terminal_id_not_present_in_request"})
		return
	}
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID, ok := s.requireTenantID(c)
	if !ok {
		return
	}
	var stats MerchantTransactions
	stmt := `SELECT
		COALESCE(SUM(tran_amount), 0) AS purchase_amount,
		COUNT(*) AS all_transactions,
		COALESCE(SUM(CASE WHEN response_status = 'Successful' THEN 1 ELSE 0 END), 0) AS successful_transactions,
		COALESCE(SUM(CASE WHEN response_status != 'Successful' THEN 1 ELSE 0 END), 0) AS failed_transactions
		FROM transactions
		WHERE tenant_id = ? AND terminal_id = ?`
	if err := db.GetContext(c.UserContext(), &stats, db.Rebind(stmt), tenantID, tid); err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	jsonResponse(c, http.StatusOK, fiber.Map{"result": stats})
}
