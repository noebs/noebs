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

type PurchaseModel struct {
	gorm.Model
	ebs_fields.PurchaseFields
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
