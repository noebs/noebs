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



type Env struct {
	Db *gorm.DB
}

func (e *Env) GetTransactionbyID(c *gin.Context){
	var tran Transaction
	//id := c.Params.ByName("id")
	err := e.Db.Find(&tran).Error; if err != nil{
		c.AbortWithStatus(404)
	}
	c.JSON(200, gin.H{"result": tran.ID})

	defer e.Db.Close()
}



/*
func setupDB(addr string) *gorm.DB {
	db, err := gorm.Open("postgres", addr)
	if err != nil {
		panic(fmt.Sprintf("failed to connect to database: %v", err))
	}

	// Migrate the schema
	db.AutoMigrate(&model.Customer{}, &model.Order{}, &model.Product{})

	return db
}

func (s *Server) getCustomers(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var customers []model.Customer
	if err := s.db.Find(&customers).Error; err != nil {
		http.Error(w, err.Error(), errToStatusCode(err))
	} else {
		writeJSONResult(w, customers)
	}
}

*/

//type ChangePIN struct {
//	gorm.Model
//	ebs_fields.ChangePinFields
//}
//
//type CardTransfer struct {
//	gorm.Model
//	ebs_fields.CardTransferFields
//}
//
//type BillPayment struct {
//	gorm.Model
//	ebs_fields.BillPaymentFields
//}
//
//type BillInquiry struct {
//	gorm.Model
//	ebs_fields.BillPaymentFields
//}
