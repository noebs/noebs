package ebs_fields

import (
	"gopkg.in/go-playground/validator.v9"
	"time"
)

// not sure this would work. This package is just for storing struct representations
// of each httpHandler

type IsAliveFields struct {
	CommonFields
}
type WorkingKeyFields struct {
	CommonFields
}

type BalanceFields struct {
	CommonFields
	CardInfoFields
}
type MiniStatementFields struct {
	CommonFields
	CardInfoFields
}
type ChangePINFields struct {
	CommonFields
	CardInfoFields
	NewPIN string `json:"newPIN" binding:"required"`
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

type CashInFields struct {
	PurchaseFields
}
type CashOutFields struct {
	PurchaseFields
}
type RefundFields struct {
	PurchaseFields
}
type PurchaseWithCashBackFields struct {
	PurchaseFields
}
type ReverseFields struct {
	PurchaseFields
}

type BillInquiryFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	BillerFields
}

type CommonFields struct {
	SystemTraceAuditNumber int    `json:"systemTraceAuditNumber,omitempty" binding:"required" form:"systemTraceAuditNumber"`
	TranDateTime           string `json:"tranDateTime,omitempty" binding:"required" form:"tranDateTime"`
	TerminalID             string `json:"terminalId,omitempty" binding:"required,len=8" form:"terminalId"`
	ClientID               string `json:"clientId,omitempty" binding:"required" form:"clientId"`
}

type CardInfoFields struct {
	Pan     string `json:"PAN" binding:"required" form:"PAN"`
	Pin     string `json:"PIN" binding:"required" form:"PIN"`
	Expdate string `json:"expDate" binding:"required" form:"expDate"`
}

type AmountFields struct {
	TranAmount       float32 `json:"tranAmount" binding:"required" form:"tranAmount"`
	TranCurrencyCode string  `json:"tranCurrencyCode" form:"tranCurrencyCode"`
}

type BillerFields struct {
	PersonalPaymentInfo string `json:"personalPaymentInfo" binding:"required" form:"personalPaymentInfo"`
	PayeeID             string `json:"payeeId" binding:"required" form:"payeeId"`
}

type PayeesListFields struct {
	CommonFields
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

	TerminalID             string `json:"terminalId,omitempty"`
	SystemTraceAuditNumber int    `json:"systemTraceAuditNumber,omitempty"`
	ClientID               string `json:"clientId,omitempty"`
	PAN                    string `json:"PAN,omitempty"`

	ServiceID        string  `json:"serviceId,omitempty"`
	TranAmount       float32 `json:"tranAmount,omitempty"`
	PhoneNumber      string  `json:"phoneNumber,omitempty"`
	FromAccount      string  `json:"fromAccount,omitempty"`
	ToAccount        string  `json:"toAccount,omitempty"`
	FromCard         string  `json:"fromCard,omitempty"`
	ToCard           string  `json:"toCard,omitempty"`
	OTP              string  `json:"otp,omitempty"`
	OTPID            string  `json:"otpId,omitempty"`
	TranCurrencyCode string  `json:"tranCurrencyCode,omitempty"`
	EBSServiceName   string
	WorkingKey       string `json:"workingKey,omitempty" gorm:"-"`
}

type ImportantEBSFields struct {
	ResponseMessage      string  `json:"responseMessage,omitempty"`
	ResponseStatus       string  `json:"responseStatus,omitempty"`
	ResponseCode         int     `json:"responseCode"`
	ReferenceNumber      string  `json:"referenceNumber,omitempty"`
	ApprovalCode         string  `json:"approvalCode,omitempty"`
	VoucherNumber        int     `json:"voucherNumber,omitempty"`
	MiniStatementRecords string  `json:"miniStatementRecords,omitempty"`
	DisputeRRN           string  `json:"DisputeRRN,omitempty"`
	AdditionalData       string  `json:"additionalData,omitempty"`
	TranDateTime         string  `json:"tranDateTime,omitempty"`
	TranFee              float32 `json:"tranFee,omitempty"`
	AdditionalAmount     float32 `json:"additionalAmount,omitempty"`
}

type ConsumerSpecificFields struct {
	UUID            string  `json:"UUID" form:"UUID" binding:"required,len=36"`
	Mbr             string  `json:"mbr,omitempty" form:"mbr"`
	Ipin            string  `json:"IPIN" form:"IPIN" binding:"required"`
	FromAccountType string  `json:"fromAccountType" form:"fromAccountType"`
	ToAccountType   string  `json:"toAccountType" form:"toAccountType"`
	AccountCurrency string  `json:"accountCurrency" form:"accountCurrency"`
	AcqTranFee      float32 `json:"acqTranFee" form:"acqTranFee"`
	IssuerTranFee   float32 `json:"issuerTranFee" form:"issuerTranFee"`

	// billers
	BillInfo string `json:"billInfo" form:"billInfo"`
	Payees   string `json:"payees" form:"payees"`

	// tran time
	OriginalTranUUID     string `json:"originalTranUUID" form:"originalTranUUID"`
	OriginalTranDateTime string `json:"originalTranDateTime" form:"originalTranDateTime"`

	// User settings
	Username     string `json:"userName" Form:"userName"`
	UserPassword string `json:"userPassword" form:"userPassword"`

	// Entities
	EntityType  string `json:"entityType" form:"entityType"`
	EntityId    string `json:"entityId" form:"entityId"`
	EntityGroup string `json:"entityGroup" form:"entityGroup"`
	PubKeyValue string `json:"pubKeyValue" form:"pubKeyValue"`
	Email       string `json:"email" form:"email"`
	ExtraInfo   string `json:"extraInfo" form:"extraInfo"`
	//.. omitted fields

	PhoneNo                string `json:"phoneNo" form:"phoneNo"`
	NewIpin                string `json:"newIPIN" form:"newIPIN"`
	NewUserPassword        string `json:"newUserPassword" form:"newUserPassword"`
	SecurityQuestion       string `json:"securityQuestion" form:"securityQuestion"`
	SecurityQuestionAnswer string `json:"securityQuestionAnswer" form:"securityQuestionAnswer"`
	AdminUserName          string `json:"adminUserName" form:"adminUserName"`

	// other fields
	OriginalTransaction map[string]interface{} `json:"originalTransaction" form:"originalTransaction"`
	OriginalTranType    string                 `json:"originalTranType" form:"originalTranType"`
}

// MaskPAN returns the last 4 digit of the PAN. We shouldn't care about the first 6
func (res *GenericEBSResponseFields) MaskPAN() {
	if res.PAN != "" {
		length := len(res.PAN)
		res.PAN = res.PAN[length-4 : length]
	}

	if res.ToCard != "" {
		length := len(res.ToCard)
		res.ToCard = res.ToCard[length-4 : length]
	}

	if res.FromCard != "" {
		length := len(res.FromCard)
		res.FromCard = res.FromCard[length-4 : length]
	}
}

type ConsumerCommonFields struct {
	ApplicationId string `json:"applicationId" form:"applicationId"`
	TranDateTime  string `json:"tranDateTime" form:"tranDateTime"`
	UUID          string `json:"UUID" form:"UUID"`
}

type ConsumerBillInquiryFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
}

type ConsumerCardHolderFields struct {
	Pan     string `json:"PAN" form:"PAN"`
	Ipin    string `json:"IPIN" form:"IPIN"`
	ExpDate string `json:"expDate" form:"expDate"`
}

type ConsumerIsAliveFields struct {
	ConsumerCommonFields
}

type ConsumerBalanceFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
}
type ConsumersBillersFields struct {
	PayeeId     string `json:"payeeId" form:"payeeId"`
	PaymentInfo string `json:"paymentInfo" form:""`
}

type ConsumerPurchaseFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	ServiceProviderId string `json:"serviceProviderId" binding:"required"`
}

type ConsumerBillPaymentFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	ConsumersBillersFields
}

type ConsumerWorkingKeyFields struct {
	ConsumerCommonFields
}

type ConsumerCardTransferFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	ToCard string `json:"toCard" binding:"required"`
}

type ConsumerStatusFields struct {
	CommonFields
	OriginalTranUUID string `json:"originalTranUUID" binding:"required"`
}
