package merchant

import (
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
)

//Init populates merchant with db pointer
func (m *Merchant) Init(db *gorm.DB, log *logrus.Logger) {
	m.db = db
	m.log = log
	m.db.AutoMigrate()
}

//CreateMerchant creates new noebs merchant
func (m Merchant) CreateMerchant(c *gin.Context) {
	if err := c.ShouldBindJSON(&m); err != nil {
		verr := ebs_fields.ValidationError{Code: "request_error", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}
	if err := m.write(); err != nil {
		verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"result": "ok"})
}

//GetMerchant from existing merchants in noebs
func (m Merchant) GetMerchant(c *gin.Context) {
	id := c.DefaultQuery("id", "")

	merchant, err := m.get(id)
	if err != nil {
		verr := ebs_fields.ValidationError{Code: "request_error", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}
	c.JSON(http.StatusOK, merchant)
}
