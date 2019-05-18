package dashboard

import (
	"github.com/adonese/noebs/ebs_fields"
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

func TransactionByTid(c *gin.Context) {

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

	//tid, _ := c.GetQuery("tid")

	var tran []Transaction
	if err := env.Db.Find(&tran).Error; err != nil {
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
			ImportantEBSFields: ebs_fields.ImportantEBSFields{
				ResponseMessage:      "",
				ResponseStatus:       "",
				ResponseCode:         0,
				ReferenceNumber:      "",
				ApprovalCode:         "",
				VoucherNumber:        0,
				MiniStatementRecords: "",
				DisputeRRN:           "",
				AdditionalData:       "",
				TranDateTime:         "",
				TranFee:              0,
				AdditionalAmount:     0,
			},
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

	env := &Env{Db: db}

	defer env.Db.Close()

	db.AutoMigrate(&Transaction{})

	qparam, _ := c.GetQuery("page")
	if qparam == "" {
		qparam = "1" // first db id
	}
	p, _ := strconv.Atoi(qparam)

	var tran []Transaction
	// just really return anything, even empty ones.
	// or, not?

	//env.Db.Order("id desc").Limit(p + limit).Find(&tran)
	db.Order("id desc").Offset(p).Limit(50).Find(&tran)

	paging := map[string]interface{}{
		"previous": "",
		"after":    "",
	}
	c.JSON(200, gin.H{"result": tran, "paging": paging})
}

// pagination handles prev - next cases
func pagination() {

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
	t := time.Now()
	today := t.Format(dateFormat)
	yesterday, _ := time.Parse(dateFormat, today)
	yesterday = yesterday.Add(-8 * 24 * time.Hour)
	todayDate, _ := time.Parse(dateFormat, today)

	db.Where("created_at BETWEEN ? AND ?", todayDate, yesterday).Find(&tran)
	//db.Model(&PurchaseModel{}).Find(&tran)

	//rows, err := db.Model(&PurchaseModel{}).Select("date(created_at) as date, sum(amount) as total").Group("date(created_at)").Rows()
	//if err != nil {
	//	log.WithFields(logrus.Fields{
	//		"error": "Error in counting results",
	//		"message": err.Error(),
	//	}).Info("unable to count purchase settlement results")
	//}
	//for rows.Next() {
	//
	//}

	p := make(purchasesSum)
	var listP []purchasesSum
	var sum float32
	for _, v := range tran {
		p["date"] = v.TranDateTime
		p["amount"] = v.TranAmount
		listP = append(listP, p)
		sum += v.TranAmount
	}
	c.JSON(http.StatusOK, gin.H{"transactions": listP, "sum": sum})
}
