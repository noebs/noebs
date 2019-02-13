package dashboard

import (
	"github.com/adonese/noebs/validations"
	"github.com/jinzhu/gorm"
)

type Transaction struct {
	gorm.Model
	validations.GenericEBSResponseFields
}

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
