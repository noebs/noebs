package dashboard

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

type Transaction struct {
	gorm.Model
	ebs_fields.GenericEBSResponseFields
}

// MaskPAN returns the last 4 digit of the PAN. We shouldn't care about the first 6
func (t *Transaction) MaskPAN() {
	if t.PAN != "" {
		length := len(t.PAN)
		t.PAN = t.PAN[length-4 : length]
	}
}

type Env struct {
	Db *gorm.DB
}

func (e *Env) GetTransactionbyID(c *gin.Context) {
	var tran Transaction
	//id := c.Params.ByName("id")
	err := e.Db.Find(&tran).Error
	if err != nil {
		c.AbortWithStatus(404)
	}
	c.JSON(200, gin.H{"result": tran.ID})

	defer e.Db.Close()
}
