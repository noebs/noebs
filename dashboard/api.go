package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/go-redis/redis/v7"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var log = logrus.New()

type Service struct {
	Redis *redis.Client
	Db    *gorm.DB
}

func (s Service) calculateOffset(page, pageSize int) uint {
	if page == 0 {
		page++
	}
	return uint((page - 1) * pageSize)
}

// MerchantViews deprecated in favor of using the react-based dashboard features.
func (s *Service) MerchantViews(c *fiber.Ctx) {
	db, _ := utils.Database("test.db")
	terminal := c.Params("id")

	pageSize := 50
	page := c.Query("page", "0")
	p, _ := strconv.Atoi(page)
	offset := p*pageSize - pageSize

	var tran []ebs_fields.EBSResponse
	db.Table("transactions").Where("id >= ? and terminal_id LIKE ? and approval_code != ?", offset, "%"+terminal+"%", "").Order("id desc").Limit(pageSize).Find(&tran)
	// get complaints

	com, _ := s.Redis.LRange("complaints", 0, -1).Result()

	var mC []merchantsIssues
	var m merchantsIssues

	for _, iss := range com {
		json.Unmarshal([]byte(iss), &m)
		mC = append(mC, m)
	}

	_ = c.Render("merchants", fiber.Map{"tran": tran, "issues": mC}, "base")

	//TODO get merchant profile

}

func (s *Service) TransactionsCount(c *fiber.Ctx) {

	db, err := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	if err != nil {
		log.WithFields(
			logrus.Fields{
				"code":    err.Error(),
				"details": "error in database",
			}).Info("error in database")
	}

	env := &Env{Db: db}

	var tran ebs_fields.EBSResponse
	var count int64

	if err := env.Db.Model(&tran).Count(&count).Error; err != nil {
		log.WithFields(
			logrus.Fields{
				"code":    err.Error(),
				"details": "error in database",
			}).Info("error in database")
		c.SendStatus(404)
	}

	jsonResponse(c, http.StatusOK, fiber.Map{"result": count})
}

func (s *Service) TransactionByTid(c *fiber.Ctx) {

	db, err := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	if err != nil {
		log.WithFields(
			logrus.Fields{
				"code":    err.Error(),
				"details": "error in database",
			}).Info("error in database")
	}

	tid := c.Query("tid")

	var tran []ebs_fields.EBSResponse
	if err := db.Where("terminal_id LIKE ?", tid+"%").Find(&tran).Error; err != nil {
		log.WithFields(logrus.Fields{
			"code":    err.Error(),
			"details": tran,
		}).Info("no transaction with this ID")
		c.SendStatus(404)
	}

	jsonResponse(c, http.StatusOK, fiber.Map{"result": tran, "count": len(tran)})
}

func (s *Service) MakeDummyTransaction(c *fiber.Ctx) {

	db, _ := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})

	env := &Env{Db: db}

	tran := ebs_fields.EBSResponse{}

	if err := env.Db.Create(&tran).Error; err != nil {
		jsonResponse(c, 500, fiber.Map{"code": err.Error()})
	} else {
		jsonResponse(c, 200, fiber.Map{"message": "object create successfully."})
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
	tran, count := sortTable(s.Db, searchField, search, sortField, sortCase, int(offset), pageSize)

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
	db, _ := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})

	var tran ebs_fields.EBSResponse
	if err := db.Where("id = ?", id).First(&tran).Error; err != nil {
		c.SendStatus(404)
	} else {
		jsonResponse(c, http.StatusOK, fiber.Map{"result": tran})
	}
}

func (s *Service) BrowserDashboard(c *fiber.Ctx) {
	db, _ := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})

	var page int

	q := c.Query("page", "1")
	page, _ = strconv.Atoi(q)

	//todo make a pagination funct(s *Service)ion
	pageSize := 50
	offset := page*pageSize - pageSize
	log.Printf("The offset is: %v", offset)
	var tran []ebs_fields.EBSResponse

	var count int64

	var search SearchModel
	var totAmount dashboardStats

	var mStats []merchantStats
	var leastMerchants []merchantStats
	var terminalFees []merchantStats

	db.Table("transactions").Count(&count)
	db.Table("transactions").Select("sum(tran_amount) as amount").Scan(&totAmount)

	if err := parseJSON(c, &search); err == nil {
		db.Table("transactions").Where("id >= ? and terminal_id LIKE ?", offset, "%"+search.TerminalID+"%").Order("id desc").Limit(pageSize).Find(&tran)
	} else {
		db.Table("transactions").Where("id >= ?", offset).Limit(pageSize).Find(&tran)
	}

	// get the most transactions per terminal_id
	// choose interval, which should be *this* month
	month := time.Now().Month()
	m := fmt.Sprintf("%02d", int(month))

	db.Table("transactions").Select("sum(tran_amount) as amount, terminal_id, datetime(created_at) as time").Where("strftime('%m', time) = ?", m).Group("terminal_id").Order("amount desc").Scan(&mStats)
	db.Table("transactions").Select("count(tran_amount) as amount, response_status, terminal_id, datetime(created_at) as time").Where("tran_amount >= ? AND response_status = ? AND strftime('%m', time) = ?", 1, "Successful", m).Group("terminal_id").Order("amount").Scan(&leastMerchants)
	db.Table("transactions").Select("count(tran_fee) as amount, response_status, terminal_id, datetime(created_at) as time").Where("tran_amount >= ? AND response_status = ? AND strftime('%m', time) = ?", 1, "Successful", m).Group("terminal_id").Order("amount desc").Scan(&terminalFees)

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

	data, err := s.getLastTransactions(q)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error()})
		return
	}

	_ = c.Render("qr_status", fiber.Map{"transactions": data}, "base")
}

func (s *Service) getLastTransactions(merchantID string) ([]ebs_fields.QRPurchase, error) {
	var transactions []ebs_fields.QRPurchase

	data, err := s.Redis.HGet(merchantID, "data").Result()
	if err != nil {
		log.Printf("erorr in redis: %v", err)
		return transactions, err
	}
	if err := json.Unmarshal([]byte(data), &transactions); err != nil {
		log.Printf("erorr in redis: %v", err)
		return transactions, err
	}
	log.Printf("the result is: %v", transactions)
	return transactions, nil
}

func (s *Service) IndexPage(c *fiber.Ctx) {
	_ = c.Render("index", fiber.Map{}, "base")
}

func (s *Service) Stream(c *fiber.Ctx) {
	var trans []ebs_fields.EBSResponse
	var stream bytes.Buffer

	db, _ := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	db.Table("transactions").Find(&trans)
	json.NewEncoder(&stream).Encode(trans)

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

	v, err := s.Redis.LRange(tid+":purchase", 0, -1).Result()
	if err != nil {
		jsonResponse(c, http.StatusOK, fiber.Map{"result": MerchantTransactions{}})
		return
	}
	sum := purchaseSum(v)
	failedTransactions, _ := s.Redis.Get(tid + ":failed_transactions").Result()
	successfulTransactions, _ := s.Redis.Get(tid + ":successful_transactions").Result()
	numberTransactions, _ := s.Redis.Get(tid + ":number_purchase_transactions").Result()
	failed, _ := strconv.Atoi(failedTransactions)
	succ, _ := strconv.Atoi(successfulTransactions)
	num, _ := strconv.Atoi(numberTransactions)

	p := MerchantTransactions{PurchaseAmount: sum, FailedTransactions: failed, SuccessfulTransactions: succ,
		AllTransactions: num}
	jsonResponse(c, http.StatusOK, fiber.Map{"result": p})
}

func (s *Service) ReportIssueEndpoint(c *fiber.Ctx) {
	var issue merchantsIssues
	if err := parseJSON(c, &issue); err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "terminalId_not_provided", "message": "Pls provide terminal Id"})
	} else {
		s.Redis.LPush("complaints", &issue)
		s.Redis.LPush(issue.TerminalID+":complaints", &issue)
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
