package dashboard

import (
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

// TransactionCount godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param id query string false "search list transactions by terminal ID"
// @Success 200 {string} ok
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /dashboard/count [get]
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

// TransactionByTid godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param id query string false "search list transactions by terminal ID"
// @Success 200 {string} ok
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /dashboard/get_tid [get]
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

	pageSize := 50
	offset := page*pageSize - pageSize

	fmt.Println(offset)
	var tran []Transaction

	// another good alternative
	db.Table("transactions").Where("id >= ?", offset).Limit(pageSize).Find(&tran)

	previous := page - 1
	next := page + 1

	paging := map[string]interface{}{
		"previous": previous,
		"after":    next,
	}
	c.JSON(http.StatusOK, gin.H{"result": tran, "paging": paging})
}

const (
	dateFormat = "2006-01-02"
)

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
		p["amount"] = v.TranAmount
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
	c.JSON(http.StatusOK, gin.H{"result": sum})
}
