package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"time"
)

var log = logrus.New()

func MerchantViews(c *gin.Context) {
	db, _ := utils.Database("sqlite3", "test.db")
	terminal := c.Param("id")

	pageSize := 50
	page := c.DefaultQuery("page", "0")
	p, _ := strconv.Atoi(page)
	offset := p*pageSize - pageSize

	var tran []Transaction
	db.Table("transactions").Where("id >= ? and terminal_id LIKE ? and approval_code != ?", offset, "%"+terminal+"%", "").Order("id desc").Limit(pageSize).Find(&tran)
	// get complaints
	redisClient := utils.GetRedis()
	com, _ := redisClient.LRange("complaints", 0, -1).Result()

	var mC []merchantsIssues
	var m merchantsIssues

	for _, iss := range com {
		json.Unmarshal([]byte(iss), &m)
		mC = append(mC, m)
	}

	c.HTML(http.StatusOK, "merchants.html", gin.H{"tran": tran, "issues": mC})

	//TODO get merchant profile

}

func TransactionsCount(c *gin.Context) {

	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil {
		log.WithFields(
			logrus.Fields{
				"error":   err.Error(),
				"details": "error in database",
			}).Info("error in database")
	}

	env := &Env{Db: db}

	defer env.Db.Close()

	if err := db.AutoMigrate(&Transaction{}).Error; err != nil {
		log.WithFields(
			logrus.Fields{
				"error":   err.Error(),
				"details": "error in database",
			}).Info("error in database")
	}

	var tran Transaction
	var count interface{}

	if err := env.Db.Model(&tran).Count(&count).Error; err != nil {
		log.WithFields(
			logrus.Fields{
				"error":   err.Error(),
				"details": "error in database",
			}).Info("error in database")
		c.AbortWithStatus(404)
	}

	c.JSON(http.StatusOK, gin.H{"result": count})
}

func TransactionByTid(c *gin.Context) {

	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil {
		log.WithFields(
			logrus.Fields{
				"error":   err.Error(),
				"details": "error in database",
			}).Info("error in database")
	}

	defer db.Close()

	if err := db.AutoMigrate(&Transaction{}).Error; err != nil {
		log.WithFields(
			logrus.Fields{
				"error":   err.Error(),
				"details": "error in database",
			}).Info("error in database")
	}

	tid, _ := c.GetQuery("tid")

	var tran []Transaction
	if err := db.Where("terminal_id LIKE ?", tid+"%").Find(&tran).Error; err != nil {
		log.WithFields(logrus.Fields{
			"error":   err.Error(),
			"details": tran,
		}).Info("no transaction with this ID")
		c.AbortWithStatus(404)
	}

	c.JSON(http.StatusOK, gin.H{"result": tran, "count": len(tran)})
}

func MakeDummyTransaction(c *gin.Context) {

	db, _ := gorm.Open("sqlite3", "test.db")

	env := &Env{Db: db}

	if err := env.Db.AutoMigrate(&Transaction{}).Error; err != nil {
		log.Fatalf("unable to automigrate: %s", err.Error())
	}

	tran := Transaction{

		Model: gorm.Model{},
		GenericEBSResponseFields: ebs_fields.GenericEBSResponseFields{
			TerminalID:             "",
			SystemTraceAuditNumber: 0,
			ClientID:               "",
			PAN:                    "",
			ServiceID:              "",
			TranAmount:             0,
			PhoneNumber:            "",
			FromAccount:            "",
			ToAccount:              "",
			FromCard:               "",
			ToCard:                 "",
			OTP:                    "",
			OTPID:                  "",
			TranCurrencyCode:       "",
			EBSServiceName:         "",
			WorkingKey:             "",
			PubKeyValue:            "",
			UUID:                   "",
			ResponseMessage:        "",
			ResponseStatus:         "",
			ResponseCode:           0,
			ReferenceNumber:        "",
			ApprovalCode:           "",
			VoucherNumber:          0,
			MiniStatementRecords:   "",
			DisputeRRN:             "",
			AdditionalData:         "",
			TranDateTime:           "",
			TranFee:                nil,
			AdditionalAmount:       nil,
		},
	}

	if err := env.Db.Create(&tran).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
	} else {
		c.JSON(200, gin.H{"message": "object create successfully."})
	}
}

func GetAll(c *gin.Context) {
	db, _ := gorm.Open("sqlite3", "test.db")

	defer db.Close()

	db.AutoMigrate(&Transaction{})

	var page int
	if q := c.Query("page"); q != "" {
		page, _ = strconv.Atoi(q)
	} else {
		page = 1
	}

	// page represents a 30 result from the database.
	// the computation should be done like this:
	// offset = page * 50
	// limit = offset + 50

	//todo make a pagination function
	pageSize := 50
	offset := page*pageSize - pageSize

	fmt.Println(offset)
	var tran []Transaction

	// another good alternative
	db.Table("transactions").Where("id >= ?", offset).Limit(pageSize).Find(&tran)

	// check whether we are accessing it from a browser
	previous := page - 1
	next := page + 1

	paging := map[string]interface{}{
		"previous": previous,
		"after":    next,
	}
	c.JSON(http.StatusOK, gin.H{"result": tran, "paging": paging})
}

func BrowserDashboard(c *gin.Context) {
	db, _ := gorm.Open("sqlite3", "test.db")

	defer db.Close()

	db.AutoMigrate(&Transaction{})

	var page int
	if q := c.Query("page"); q != "" {
		page, _ = strconv.Atoi(q)
	} else {
		page = 1
	}

	//todo make a pagination function
	pageSize := 50
	offset := page*pageSize - pageSize

	var tran []Transaction

	var count int

	var search SearchModel
	var totAmount dashboardStats

	var mStats []merchantStats
	var leastMerchants []merchantStats
	var terminalFees []merchantStats

	db.Table("transactions").Count(&count)
	db.Table("transactions").Select("sum(tran_amount) as amount").Scan(&totAmount)

	if c.ShouldBind(&search) == nil {
		db.Table("transactions").Where("id >= ? and terminal_id LIKE ?", offset, "%"+search.TerminalID+"%").Order("id desc").Limit(pageSize).Find(&tran)
	} else {
		db.Table("transactions").Where("id >= ?", offset).Order("id desc").Limit(pageSize).Find(&tran)
	}

	// get the most transactions per terminal_id
	// choose interval, which should be *this* month
	month := time.Now().Month()
	m := fmt.Sprintf("%02d", int(month))

	db.Table("transactions").Select("sum(tran_amount) as amount, terminal_id, datetime(created_at) as time").Where("strftime('%m', time) = ?", m).Group("terminal_id").Order("amount desc").Scan(&mStats)
	db.Table("transactions").Select("count(tran_amount) as amount, response_status, terminal_id, datetime(created_at) as time").Where("tran_amount >= ? AND response_status = ? AND strftime('%m', time) = ?", 1, "Successful", m).Group("terminal_id").Order("amount").Scan(&leastMerchants)
	db.Table("transactions").Select("count(tran_fee) as amount, response_status, terminal_id, datetime(created_at) as time").Where("tran_amount >= ? AND response_status = ? AND strftime('%m', time) = ?", 1, "Successful", m).Group("terminal_id").Order("amount desc").Scan(&terminalFees)

	log.Printf("the least merchats are: %v", leastMerchants)
	pager := pagination(count, 50)
	errors := errorsCounter(tran)
	stats := map[string]int{
		"NumberTransactions":     count,
		"SuccessfulTransactions": count - errors,
		"FailedTransactions":     errors,
	}

	sumFees := computeSum(terminalFees)
	c.HTML(http.StatusOK, "table.html", gin.H{"transactions": tran, "count": pager + 1,
		"stats": stats, "amounts": totAmount, "merchant_stats": mStats, "least_merchants": leastMerchants, "terminal_fees": terminalFees, "sum_fees": sumFees})
}

func LandingPage(c *gin.Context) {
	showForm := true
	if c.Request.Method == "POST" {
		var f form
		err := c.ShouldBind(&f)
		if err == nil {
			ua := c.GetHeader("User-Agent")
			redisClient := utils.GetRedis()
			redisClient.LPush("voices", &f)
			redisClient.LPush("voices:ua", ua)
			showForm = false
		}
	}

	c.HTML(http.StatusOK, "landing.html", gin.H{"showform": showForm})
}

func MerchantRegistration(c *gin.Context) {
	var f ebs_fields.Merchant
	if c.Request.Method == "POST" {
		err := c.ShouldBind(&f)
		if err == nil {
			redisClient := utils.GetRedis()
			redisClient.SAdd("merchants:all", f.MerchantName)
			redisClient.HMSet("merchant:"+f.MerchantName, f.ToMap())
			c.HTML(http.StatusOK, "landing.html", gin.H{"showform": false})
		} else {
			er, _ := c.Errors.MarshalJSON()
			log.Printf("Errors are: %s, and the binding err: %v", string(er), err.Error())
		}
	} else if c.Request.Method == "GET" {
		fields := f.Details()
		c.HTML(http.StatusOK, "merchant_registration.html", gin.H{"showform": true, "fields": fields})
	}
}

func IndexPage(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", nil)
}

func Stream(c *gin.Context) {
	var trans []Transaction
	var stream bytes.Buffer

	db, _ := gorm.Open("sqlite3", "test.db")

	defer db.Close()

	db.AutoMigrate(&Transaction{})
	db.Table("transactions").Find(&trans)
	json.NewEncoder(&stream).Encode(trans)

	extraHeaders := map[string]string{
		"Content-Disposition": `attachment; filename="transactions.json"`,
	}
	c.DataFromReader(http.StatusOK, int64(stream.Len()), "application/octet-stream", &stream, extraHeaders)

}

type purchasesSum map[string]interface{}

// This endpoint is highly experimental. It has many security issues and it is only
// used by us for testing and prototyping only. YOU HAVE TO USE PROPER AUTHENTICATION system
// if you decide to go with it. See apigateway package if you are interested.
func DailySettlement(c *gin.Context) {
	// get the results from DB
	db, _ := gorm.Open("sqlite3", "test.db")
	defer db.Close()

	db.AutoMigrate(&PurchaseModel{})
	var tran []PurchaseModel

	q, _ := c.GetQuery("terminal")
	if q == "" {
		// case of empty terminal
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "empty terminal ID", "code": "empty_terminal_id"})
		return
	}
	format := "2006-10-12"
	today := time.Now().Format(format)
	t, _ := time.Parse(format, today)
	yesterday := t.Add(-23 * time.Hour).Add(-59 * time.Minute).Add(-59 * time.Second)

	db.Where("created_at BETWEEN ? AND ? AND terminal_id = ?", yesterday, t, q).Find(&tran)

	p := make(purchasesSum)
	var listP []purchasesSum
	var sum float32
	count := len(tran)
	for _, v := range tran {
		p["date"] = v.TranDateTime
		p["Amount"] = v.TranAmount
		p["human_readable_time"] = v.CreatedAt

		listP = append(listP, p)
		sum += v.TranAmount
	}
	c.JSON(http.StatusOK, gin.H{"transactions": listP, "sum": sum, "count": count})
}

func MerchantTransactionsEndpoint(c *gin.Context) {
	tid := c.Query("terminal")
	if tid == "" {
		// the user didn't sent any id
		c.JSON(http.StatusBadRequest, gin.H{"message": "terminal id not present in url params",
			"code": "terminal_id_not_present_in_request"})
		return
	}
	redisClient := utils.GetRedis()
	v, err := redisClient.LRange(tid+":purchase", 0, -1).Result()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"result": MerchantTransactions{}})
		return
	}
	sum := purchaseSum(v)
	failedTransactions, _ := redisClient.Get(tid + ":failed_transactions").Result()
	successfulTransactions, _ := redisClient.Get(tid + ":successful_transactions").Result()
	numberTransactions, _ := redisClient.Get(tid + ":number_purchase_transactions").Result()
	failed, _ := strconv.Atoi(failedTransactions)
	succ, _ := strconv.Atoi(successfulTransactions)
	num, _ := strconv.Atoi(numberTransactions)

	p := MerchantTransactions{PurchaseAmount: sum, FailedTransactions: failed, SuccessfulTransactions: succ,
		AllTransactions: num}
	c.JSON(http.StatusOK, gin.H{"result": p})
}

func ReportIssueEndpoint(c *gin.Context) {
	var issue merchantsIssues
	if err := c.ShouldBindJSON(&issue); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "terminalId_not_provided", "message": "Pls provide terminal Id"})
	} else {
		redisClient := utils.GetRedis()
		redisClient.LPush("complaints", &issue)
		redisClient.LPush(issue.TerminalID+":complaints", &issue)
		c.JSON(http.StatusOK, gin.H{"result": "ok"})
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
