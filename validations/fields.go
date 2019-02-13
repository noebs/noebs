package validations

import (
	"gopkg.in/go-playground/validator.v9"
	"time"
)

// not sure this would work. This package is just for storing struct representations
// of each httpHandler

type WorkingKeyFields struct {
	CommonFields
}

type ChangePINFields struct {
	CommonFields
	CardInfoFields
	NewPIN string `json:"newPin" binding:"required"`
}

type CardTransferFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	ToCard string `json:"toCard" binding:"required"`
}

type PurchaseFields struct {
	WorkingKeyFields
	CardInfoFields
	AmountFields
}

type BillPaymentFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	BillerFields
}

type CashInFields struct{}
type CashOutFields struct{}
type RefundFields struct{}
type PurchaseWithCashBackFields struct{}
type ReverseFields struct{}

type BillInquiryFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	BillerFields
}

type CommonFields struct {
	SystemTraceAuditNumber int    `json:"systemTraceAuditNumber,omitempty" binding:"required"`
	TranDateTime           string `json:"tranDateTime,omitempty" binding:"required,iso8601"`
	TerminalID             string `json:"terminalId,omitempty" binding:"required,len=8"`
	ClientID               string `json:"clientId,omitempty" binding:"required"`
}

type CardInfoFields struct {
	Pan     string `json:"PAN" binding:"required"`
	Pin     string `json:"PIN" binding:"required"`
	Expdate string `json:"expDate" binding:"required"`
}

type AmountFields struct {
	TranAmount       float32 `json:"tranAmount" binding:"required"`
	TranCurrencyCode string  `json:"tranCurrencyCode"`
}

type BillerFields struct {
	PersonalPaymentInfo string `json:"personalPaymentInfo" binding:"required"`
	PayeeID             string `json:"payeeId" binding:"required"`
}

func iso8601(fl validator.FieldLevel) bool {

	dateLayout := time.RFC3339
	_, err := time.Parse(dateLayout, fl.Field().String())
	if err != nil {
		return false
	}
	return true
}

type GenericEBSResponseFields struct {
	ImportantEBSFields

	TerminalID             string  `json:"terminalId" gorm:"unique_index"`
	TranDateTime           string  `json:"tranDateTime"`
	SystemTraceAuditNumber int     `json:"systemTraceAuditNumber"`
	ClientID               string  `json:"clientId" gorm:"unique_index"`
	PAN                    string  `json:"PAN"`
	AdditionalData         string  `json:"additionalData"`
	ServiceID              string  `json:"serviceId"`
	TranFee                float32 `json:"tranFee"`
	AdditionalAmount       float32 `json:"additionalAmount"`
	TranAmount             float32 `json:"tranAmount"`
	PhoneNumber            string  `json:"phoneNumber"`
	FromAccount            string  `json:"fromAccount"`
	ToAccount              string  `json:"toAccount"`
	FromCard               string  `json:"fromCard"`
	ToCard                 string  `json:"toCard"`
	OTP                    string  `json:"otp"`
	OTPID                  string  `json:"otpId"`
	TranCurrencyCode       string  `json:"tranCurrencyCode"`
	EBSServiceName         string
}

type ImportantEBSFields struct {
	ResponseMessage      string `json:"responseMessage"`
	ResponseStatus       string `json:"responseStatus"`
	ResponseCode         int    `json:"responseCode"`
	ReferenceNumber      int    `json:"referenceNumber"`
	ApprovalCode         int    `json:"approvalCode"`
	VoucherNumber        int    `json:"voucherNumber"`
	MiniStatementRecords string `json:"miniStatementRecords"`
	DisputeRRN           string `json:"DisputeRRN"`
}
