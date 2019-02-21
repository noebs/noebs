package dashboard

import (
	"github.com/adonese/noebs/validations"
	"github.com/jinzhu/gorm"
)

type Transaction struct {
	gorm.Model
	validations.GenericEBSResponseFields
}

type Server struct{
	db *gorm.DB
}

func (s *Server) GetDB() (*gorm.DB, error){
	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil{
		return nil, err
	}
	return db, nil
}
func (s *Server) Get(id string) ([]Transaction, error){

	var res []Transaction
	if err := s.db.Where(&Transaction{
		GenericEBSResponseFields: validations.GenericEBSResponseFields{TerminalID:id},
	}).Find(&res).Error; err != nil {
		return nil, err
	}
	return res, nil
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
//	validations.ChangePinFields
//}
//
//type CardTransfer struct {
//	gorm.Model
//	validations.CardTransferFields
//}
//
//type BillPayment struct {
//	gorm.Model
//	validations.BillPaymentFields
//}
//
//type BillInquiry struct {
//	gorm.Model
//	validations.BillPaymentFields
//}
