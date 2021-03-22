package merchant

import (
	"log"
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
)

//AddBilling to a specific biller ID via `MerchantMobileNumber`
func (m Merchant) AddBilling(c *gin.Context) {
	c.BindJSON(&m)
	if m.BillerID == "" || m.MerchantMobileNumber == "" {
		verr := ebs_fields.ValidationError{Code: "not_found", Message: "empty_biller"}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	if err := m.db.Exec("update merchants set biller_id = ? where mobile = ?", m.BillerID, m.MerchantMobileNumber).Error; err != nil {
		m.log.Printf("error in updating billers: %v", err)
		verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
		c.JSON(http.StatusInternalServerError, verr)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "ok"})

}

//Update to a specific biller ID via `MerchantMobileNumber`
func (m Merchant) Update(c *gin.Context) {
	c.ShouldBindJSON(&m)
	log.Printf("The merchant is: %#v", m)
	if m.BillerID == "" {
		verr := ebs_fields.ValidationError{Code: "not_found", Message: "empty_biller"}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	//TODO(adonese): omit fields in update. Could be dangerous.
	if err := m.db.Model(&Merchant{}).Omit("mobile", "merchant_id", "password", "biller_id").Where("merchant_id = ?", m.MerchantID).Updates(m).Error; err != nil {
		verr := ebs_fields.ValidationError{Code: "not_found", Message: err.Error()}

		c.JSON(http.StatusBadRequest, verr)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "ok"})

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

// Login creates new noebs merchant
func (m Merchant) Login(c *gin.Context) {
	c.ShouldBindJSON(&m)

	if m.Password == "" || m.MerchantID == "" && m.MerchantMobileNumber == "" {
		verr := ebs_fields.ValidationError{Code: "request_error", Message: "missing_fields"}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	// FIXME
	if m.MerchantMobileNumber != "" {
		merchant, err := m.getMobile(m.MerchantMobileNumber)
		if err != nil {
			verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
			c.JSON(http.StatusBadRequest, verr)
			return
		}
		c.JSON(http.StatusOK, merchant)
		return
	}
	merchant, err := m.get(m.MerchantID)
	if err != nil {
		verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}
	c.JSON(http.StatusOK, merchant)
}
