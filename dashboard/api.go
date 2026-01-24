package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

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

// MerchantViews deprecated in favor of using the react-based dashboard features.
func (s *Service) MerchantViews(c *fiber.Ctx) {
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	terminal := c.Params("id")
	tenantID := s.resolveTenantID(c)

	pageSize := 50
	page := c.Query("page", "0")
	p, _ := strconv.Atoi(page)
	if p < 1 {
		p = 1
	}
	offset := (p - 1) * pageSize

	tran, err := fetchTransactions(
		c.UserContext(),
		db,
		`SELECT id, created_at, updated_at, payload FROM transactions
		 WHERE tenant_id = ? AND terminal_id LIKE ? AND approval_code != ''
		 ORDER BY id DESC LIMIT ? OFFSET ?`,
		tenantID,
		"%"+terminal+"%",
		pageSize,
		offset,
	)
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

	issues, err := listIssues(c.UserContext(), db, tenantID, terminal)
	if err != nil {
		log.WithFields(logrus.Fields{"code": err.Error()}).Info("error loading issues")
	}

	_ = c.Render("merchants", fiber.Map{"tran": tran, "issues": issues}, "base")

	//TODO get merchant profile

}

func (s *Service) TransactionsCount(c *fiber.Ctx) {
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID := s.resolveTenantID(c)
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
	tenantID := s.resolveTenantID(c)
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
		log.WithFields(logrus.Fields{"code": err.Error()}).Info("no transaction with this ID")
		c.SendStatus(404)
		return
	}

	jsonResponse(c, http.StatusOK, fiber.Map{"result": tran, "count": len(tran)})
}

func (s *Service) MakeDummyTransaction(c *fiber.Ctx) {
	if s.Store == nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"code": "db_not_configured"})
		return
	}
	tenantID := s.resolveTenantID(c)
	tran := ebs_fields.EBSResponse{}
	if err := s.Store.CreateTransaction(c.UserContext(), tenantID, tran); err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"code": err.Error()})
	} else {
		jsonResponse(c, http.StatusOK, fiber.Map{"message": "object create successfully."})
	}
}

func (s *Service) GetAll(c *fiber.Ctx) {
	p := c.Query("page", "0")
	size := c.Query("size", "50")
	perPage := c.Query("perPage", "")

	search := c.Query("search", "")
	searchField := c.Query("field", "")
	sortField := c.Query("sort_field", "id")
	sortCase := c.Query("order", "")

	if perPage != "" {
		size = perPage
	}
	pageSize, _ := strconv.Atoi(size)
	page, _ := strconv.Atoi(p)

	offset := s.calculateOffset(page, pageSize)

	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID := s.resolveTenantID(c)
	tran, count, err := sortTable(db, tenantID, searchField, search, sortField, sortCase, int(offset), pageSize)
	if err != nil {
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
	tenantID := s.resolveTenantID(c)
	tran, err := fetchTransactions(
		c.UserContext(),
		db,
		"SELECT id, created_at, updated_at, payload FROM transactions WHERE tenant_id = ? AND id = ? LIMIT 1",
		tenantID,
		id,
	)
	if err != nil || len(tran) == 0 {
		c.SendStatus(404)
		return
	}
	jsonResponse(c, http.StatusOK, fiber.Map{"result": tran[0]})
}

func (s *Service) BrowserDashboard(c *fiber.Ctx) {
	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID := s.resolveTenantID(c)

	page := 1
	if q := c.Query("page", "1"); q != "" {
		if parsed, err := strconv.Atoi(q); err == nil && parsed > 0 {
			page = parsed
		}
	}

	pageSize := 50
	offset := (page - 1) * pageSize
	log.Printf("The offset is: %v", offset)

	var search SearchModel
	var tran []ebs_fields.EBSResponse
	var count int64
	var totAmount dashboardStats
	var mStats []merchantStats
	var leastMerchants []merchantStats
	var terminalFees []merchantStats

	if err := db.GetContext(c.UserContext(), &count, db.Rebind("SELECT COUNT(*) FROM transactions WHERE tenant_id = ?"), tenantID); err != nil {
		log.WithFields(logrus.Fields{"code": err.Error()}).Info("error counting transactions")
	}
	if err := db.GetContext(c.UserContext(), &totAmount, db.Rebind("SELECT COALESCE(SUM(tran_amount), 0) AS amount FROM transactions WHERE tenant_id = ?"), tenantID); err != nil {
		log.WithFields(logrus.Fields{"code": err.Error()}).Info("error summing transactions")
	}

	if err := parseJSON(c, &search); err == nil && search.TerminalID != "" {
		tran, err = fetchTransactions(
			c.UserContext(),
			db,
			`SELECT id, created_at, updated_at, payload FROM transactions
			 WHERE tenant_id = ? AND terminal_id LIKE ?
			 ORDER BY id DESC LIMIT ? OFFSET ?`,
			tenantID,
			"%"+search.TerminalID+"%",
			pageSize,
			offset,
		)
	} else {
		tran, err = fetchTransactions(
			c.UserContext(),
			db,
			`SELECT id, created_at, updated_at, payload FROM transactions
			 WHERE tenant_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`,
			tenantID,
			pageSize,
			offset,
		)
	}
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}

	start := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	_ = db.SelectContext(
		c.UserContext(),
		&mStats,
		db.Rebind(`SELECT terminal_id, COALESCE(SUM(tran_amount), 0) AS amount
			FROM transactions
			WHERE tenant_id = ? AND created_at >= ? AND created_at < ?
			GROUP BY terminal_id
			ORDER BY amount DESC`),
		tenantID,
		start,
		end,
	)
	_ = db.SelectContext(
		c.UserContext(),
		&leastMerchants,
		db.Rebind(`SELECT terminal_id, COUNT(*) AS amount
			FROM transactions
			WHERE tenant_id = ? AND tran_amount >= ? AND response_status = ? AND created_at >= ? AND created_at < ?
			GROUP BY terminal_id
			ORDER BY amount`),
		tenantID,
		1,
		"Successful",
		start,
		end,
	)
	_ = db.SelectContext(
		c.UserContext(),
		&terminalFees,
		db.Rebind(`SELECT terminal_id, COUNT(tran_fee) AS amount
			FROM transactions
			WHERE tenant_id = ? AND tran_amount >= ? AND response_status = ? AND created_at >= ? AND created_at < ?
			GROUP BY terminal_id
			ORDER BY amount DESC`),
		tenantID,
		1,
		"Successful",
		start,
		end,
	)

	log.Printf("the least merchats are: %v", leastMerchants)

	pager := pagination(int(count), 50)
	errors := errorsCounter(tran)
	stats := map[string]int{
		"NumberTransactions":     int(count),
		"SuccessfulTransactions": int(count) - errors,
		"FailedTransactions":     errors,
	}

	sumFees := computeSum(terminalFees)
	_ = c.Render("table", fiber.Map{"transactions": tran, "count": pager + 1,
		"stats": stats, "amounts": totAmount, "merchant_stats": mStats, "least_merchants": leastMerchants,
		"terminal_fees": terminalFees, "sum_fees": sumFees}, "base")
}

func (s *Service) QRStatus(c *fiber.Ctx) {

	q := c.Query("id")
	if q == "" {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "id is required"})
		return
	}
	tenantID := s.resolveTenantID(c)
	data, err := s.getLastTransactions(c.UserContext(), tenantID, q)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error()})
		return
	}

	_ = c.Render("qr_status", fiber.Map{"transactions": data}, "base")
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
	_ = c.Render("index", fiber.Map{}, "base")
}

func (s *Service) Stream(c *fiber.Ctx) {
	var stream bytes.Buffer

	db, err := s.ensureDB()
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	tenantID := s.resolveTenantID(c)
	trans, err := fetchTransactions(
		c.UserContext(),
		db,
		"SELECT id, created_at, updated_at, payload FROM transactions WHERE tenant_id = ? ORDER BY id DESC",
		tenantID,
	)
	if err != nil {
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
		return
	}
	_ = json.NewEncoder(&stream).Encode(trans)

	c.Set("Content-Disposition", `attachment; filename="transactions.json"`)
	c.Set("Content-Type", "application/octet-stream")
	_ = c.SendStream(&stream)

}

type purchasesSum map[string]interface{}

// This endpoint is highly experimental. It has many security issues and it is only
// used by us for testing and prototyping only. YOU HAVE TO USE PROPER AUTHENTICATION system
// if you decide to go with it. See apigateway package if you are interested.
// DEPRECATED as it is not being used and needs proper maintainance
func (s *Service) DailySettlement(c *fiber.Ctx) {
	// get the results from DB
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
	tenantID := s.resolveTenantID(c)
	var stats MerchantTransactions
	stmt := `SELECT
		COALESCE(SUM(tran_amount), 0) AS purchase_amount,
		COUNT(*) AS all_transactions,
		COALESCE(SUM(CASE WHEN response_status = 'Successful' THEN 1 ELSE 0 END), 0) AS successful_transactions,
		COALESCE(SUM(CASE WHEN response_status != 'Successful' THEN 1 ELSE 0 END), 0) AS failed_transactions
		FROM transactions
		WHERE tenant_id = ? AND terminal_id = ?`
	if err := db.GetContext(c.UserContext(), &stats, db.Rebind(stmt), tenantID, tid); err != nil {
		jsonResponse(c, http.StatusOK, fiber.Map{"result": MerchantTransactions{}})
		return
	}
	jsonResponse(c, http.StatusOK, fiber.Map{"result": stats})
}

func (s *Service) ReportIssueEndpoint(c *fiber.Ctx) {
	var issue merchantsIssues
	if err := parseJSON(c, &issue); err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "terminalId_not_provided", "message": "Pls provide terminal Id"})
	} else {
		db, err := s.ensureDB()
		if err != nil {
			jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
			return
		}
		tenantID := s.resolveTenantID(c)
		if err := insertIssue(c.UserContext(), db, tenantID, issue); err != nil {
			jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error()})
			return
		}
		jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
	}
}

//TODO
// - Add Merchant views
// - Add Merchant stats / per month

func computeSum(m []merchantStats) float32 {
	var s float32
	for _, v := range m {
		s += v.Amount
	}
	return s
}

func listIssues(ctx context.Context, db *sqlx.DB, tenantID, terminalID string) ([]merchantsIssues, error) {
	query := "SELECT terminal_id, latitude, longitude, reported_at FROM merchant_issues WHERE tenant_id = ?"
	args := []any{tenantID}
	if terminalID != "" {
		query += " AND terminal_id = ?"
		args = append(args, terminalID)
	}
	query += " ORDER BY reported_at DESC"
	var issues []merchantsIssues
	if err := db.SelectContext(ctx, &issues, db.Rebind(query), args...); err != nil {
		return nil, err
	}
	return issues, nil
}

func insertIssue(ctx context.Context, db *sqlx.DB, tenantID string, issue merchantsIssues) error {
	reportedAt := normalizeTime(issue.Time)
	stmt := `INSERT INTO merchant_issues(tenant_id, terminal_id, latitude, longitude, reported_at, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`
	_, err := db.ExecContext(ctx, db.Rebind(stmt), tenantID, issue.TerminalID, issue.Latitude, issue.Longitude, reportedAt, time.Now().UTC())
	return err
}
