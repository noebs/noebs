package dashboard

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"log"
	"math/rand"
	"strconv"
	"time"
)

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
	//FIXME
	// this function counts; it doesnt make *queries*

	db, _ := gorm.Open("sqlite3", "test.db")

	env := &Env{Db: db}

	defer env.Db.Close()

	db.AutoMigrate(&Transaction{})

	var tran Transaction
	var count interface{}
	//id := c.Params.ByName("id")
	err := env.Db.Model(&tran).Count(&count).Error
	if err != nil {
		c.AbortWithStatus(404)
	}
	c.JSON(200, gin.H{"result": count})
}

func MakeDummyTransaction(c *gin.Context) {

	db, _ := gorm.Open("sqlite3", "test.db")

	env := &Env{Db: db}

	if err := env.Db.AutoMigrate(&Transaction{}).Error; err != nil {
		log.Fatalf("unable to automigrate: %s", err.Error())
	}

	tran := Transaction{
		GenericEBSResponseFields: ebs_fields.GenericEBSResponseFields{
			ImportantEBSFields:     ebs_fields.ImportantEBSFields{},
			TerminalID:             "08000002",
			TranDateTime:           time.Now().UTC().String(),
			SystemTraceAuditNumber: rand.Intn(9999),
			ClientID:               "Morsa",
			PAN:                    "123457894647372",
			AdditionalData:         "",
			ServiceID:              "",
			TranFee:                0,
			AdditionalAmount:       0,
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

	limit := 20

	db.AutoMigrate(&Transaction{})

	qparam, ok := c.GetQuery("page")
	if !ok {
		// hack to make it works
		qparam = "0"
	}
	p, _ := strconv.Atoi(qparam)

	var tran []Transaction
	// just really return anything, even empty ones.
	// or, not?
	env.Db.Order("id desc").Limit(p+limit).Where("id = ?", p).Find(&tran)

	c.JSON(200, gin.H{"result": tran})
}
