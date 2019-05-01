package dashboard

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
	"net/http"
	"strconv"
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
				ReferenceNumber:      0,
				ApprovalCode:         0,
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

	limit, _ := c.GetQuery("limit")
	if limit == "" {
		limit = "20" // default page size
	}
	l, _ := strconv.Atoi(limit)

	var tran []Transaction
	// just really return anything, even empty ones.
	// or, not?

	//FIXME This api is not working
	//env.Db.Order("id desc").Limit(p + limit).Find(&tran)
	db.Order("id desc").Offset(p).Limit(l).Find(&tran)

	c.JSON(200, gin.H{"result": tran, "first": p, "last": p + l})
}
