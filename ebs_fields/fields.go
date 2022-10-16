// Package ebs_fields include all of ebs fields data for both consumer and merchant web services
// channels. ebs_fields package also includes endpoints and IPs for all EBS payment services.
// The package uses embed to embed secret file that includes configuration and info
// we don't like (read: legally obligated) to share, e.g., EBS IPs.
package ebs_fields

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	_ "embed"

	"github.com/go-playground/validator/v10"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NoebsDatabase is an interface that can be used throughout the codebase to make the process of many sql operations
// more seamless
type NoebsDatabase interface {
	OverrideField() string
	GetPk() string
}

type IsAliveFields struct {
	CommonFields
}

func (f *IsAliveFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type WorkingKeyFields struct {
	CommonFields
}

func (f *WorkingKeyFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type BalanceFields struct {
	CommonFields
	CardInfoFields
}

func (f *BalanceFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type MiniStatementFields struct {
	CommonFields
	CardInfoFields
}

func (f *MiniStatementFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ChangePINFields struct {
	CommonFields
	CardInfoFields
	NewPIN string `json:"newPIN" binding:"required"`
}

func (f *ChangePINFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type CardTransferFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	ToCard string `json:"toCard" binding:"required"`
}

type AccountTransferFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	ToAccount string `json:"toAccount" binding:"required"`
}

func (f *CardTransferFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type PurchaseFields struct {
	WorkingKeyFields
	CardInfoFields
	AmountFields
}

func (f *PurchaseFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type BillPaymentFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	BillerFields
}

func (f *BillPaymentFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type CashInFields struct {
	PurchaseFields
}

func (f *CashInFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type CashOutFields struct {
	PurchaseFields
}

type GenerateVoucherFields struct {
	PurchaseFields
	PhoneNumber string `json:"phoneNumber" binding:"required"`
}

type VoucherCashOutFields struct {
	CommonFields
	PhoneNumber   string  `json:"phoneNumber" binding:"required"`
	VoucherNumber string  `json:"voucherNumber" binding:"required"`
	TranAmount    float32 `json:"tranAmount" binding:"required" form:"tranAmount"`
}

type VoucherCashInFields struct {
	CommonFields
	VoucherNumber string `json:"voucherNumber" binding:"required"`
	AmountFields
	ApprovalCode string `json:"approvalCode,omitempty"`
	CardInfoFields
}

func (f *CashOutFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type RefundFields struct {
	PurchaseFields
	OriginalSTAN int `json:"originalSystemTraceAuditNumber" binding:"required"`
}

func (f *RefundFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type PurchaseWithCashBackFields struct {
	PurchaseFields
}

func (f *PurchaseWithCashBackFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ReverseFields struct {
	PurchaseFields
}

func (f *ReverseFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type BillInquiryFields struct {
	CommonFields
	CardInfoFields
	BillerFields
}

func (f *BillInquiryFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type CommonFields struct {
	SystemTraceAuditNumber int    `json:"systemTraceAuditNumber,omitempty" binding:"required" form:"systemTraceAuditNumber"`
	TranDateTime           string `json:"tranDateTime,omitempty" binding:"required" form:"tranDateTime"`
	TerminalID             string `json:"terminalId,omitempty" binding:"required,len=8" form:"terminalId"`
	ClientID               string `json:"clientId,omitempty" binding:"required" form:"clientId"`
}

// CardInfoFields implements a payment card info
type CardInfoFields struct {
	Pan     string `json:"PAN" binding:"required" form:"PAN"`
	Pin     string `json:"PIN" binding:"required" form:"PIN"`
	Expdate string `json:"expDate" binding:"required" form:"expDate"`
}

// AmountFields transaction amount data
type AmountFields struct {
	TranAmount       float32 `json:"tranAmount" binding:"required" form:"tranAmount"`
	TranCurrencyCode string  `json:"tranCurrencyCode" form:"tranCurrencyCode"`
}

type ConsumerAmountFields struct {
	TranAmount       float32 `json:"tranAmount" binding:"required" form:"tranAmount"`
	TranCurrencyCode string  `json:"tranCurrency" form:"tranCurrency"`
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
	return err == nil
}

// EBSResponse represent a struct that captures all of EBS response fields and map them into Transaction table
// We should really split this up between consumer and merchant. It is just too complicated to manage now
type EBSResponse struct {
	gorm.Model
	UserID                 uint    // userID that is associated to this transaction
	TerminalID             string  `json:"terminalId,omitempty"`
	SystemTraceAuditNumber int     `json:"systemTraceAuditNumber,omitempty"`
	ClientID               string  `json:"clientId,omitempty"`
	PAN                    string  `json:"PAN,omitempty"`
	ServiceID              string  `json:"serviceId,omitempty"`
	TranAmount             float32 `json:"tranAmount,omitempty"`
	PhoneNumber            string  `json:"phoneNumber,omitempty"`
	FromAccount            string  `json:"fromAccount,omitempty"`
	ToAccount              string  `json:"toAccount,omitempty"`
	FromCard               string  `json:"fromCard,omitempty"`
	ToCard                 string  `json:"toCard,omitempty"`
	OTP                    string  `json:"otp,omitempty"`
	OTPID                  string  `json:"otpId,omitempty"`
	TranCurrencyCode       string  `json:"tranCurrencyCode,omitempty"`
	EBSServiceName         string  `json:"-,omitempty"`
	WorkingKey             string  `json:"workingKey,omitempty" gorm:"-"`
	PayeeID                string  `json:"payeeId,omitempty"`
	// Consumer fields
	PubKeyValue     string `json:"pubKeyValue,omitempty" form:"pubKeyValue"`
	UUID            string `json:"UUID,omitempty" form:"UUID"`
	ResponseMessage string `json:"responseMessage,omitempty"`
	ResponseStatus  string `json:"responseStatus,omitempty"`
	ResponseCode    int    `json:"responseCode"`
	ReferenceNumber string `json:"referenceNumber,omitempty"`
	ApprovalCode    string `json:"approvalCode,omitempty"`
	VoucherNumber   string `json:"voucherNumber,omitempty"`
	VoucherCode     string `json:"voucherCode,omitempty"`
	//FIXME(adonese): #166 ministatement records need to be properly parsed to sqlite compatible type
	MiniStatementRecords MinistatementDB `json:"miniStatementRecords,omitempty" gorm:"type:text[]"` //make this gorm-able
	DisputeRRN           string          `json:"DisputeRRN,omitempty"`
	AdditionalData       string          `json:"additionalData,omitempty"`
	TranDateTime         string          `json:"tranDateTime,omitempty"`
	TranFee              *float32        `json:"tranFee,omitempty"`

	AdditionalAmount *float32 `json:"additionalAmount,omitempty"`
	AcqTranFee       *float32 `json:"acqTranFee,omitempty"`
	IssTranFee       *float32 `json:"issuerTranFee,omitempty"`
	TranCurrency     string   `json:"tranCurrency,omitempty"`

	// QR payment fields
	MerchantID               string  `json:"merchantID,omitempty"`
	GeneratedQR              string  `json:"generatedQR,omitempty"`
	Bank                     string  `json:"bank,omitempty"`
	Name                     string  `json:"name,omitempty"`
	CardType                 string  `json:"card_type,omitempty"`
	LastPAN                  string  `json:"last4PANDigits,omitempty"`
	TransactionID            string  `json:"transactionId,omitempty"`
	CheckDuplicate           string  `json:"checkDuplicate,omitempty"`
	AuthenticationType       string  `json:"authenticationType,omitempty"`
	AccountCurrency          string  `json:"accountCurrency,omitempty"`
	ToAccountType            string  `json:"toAccountType,omitempty"`
	FromAccountType          string  `json:"fromAccountType,omitempty"`
	EntityID                 string  `json:"entityId,omitempty"`
	EntityType               string  `json:"entityType,omitempty"`
	Username                 string  `json:"userName,omitempty"`
	DynamicFees              float64 `json:"dynamicFees,omitempty"`
	QRCode                   string  `json:"QRCode,omitempty"`
	ExpDate                  string  `json:"expDate,omitempty"` // FIXME(adonese): don't store it in database
	FinancialInstitutionID   string  `json:"financialInstitutionId,omitempty"`
	CreationDate             string  `json:"creationDate,omitempty"`
	PanCategory              string  `json:"panCategory,omitempty"`
	EntityGroup              string  `json:"entityGroup,omitempty"`
	MerchantAccountType      string  `json:"merchantAccountType,omitempty"`
	MerchantAccountReference string  `json:"merchantAccountReference,omitempty"`
	MerchantName             string  `json:"merchantName,omitempty"`
	MerchantCity             string  `json:"merchantCity,omitempty"`
	MobileNo                 string  `json:"mobileNo,omitempty"`
	MerchantCategoryCode     string  `json:"merchantCategoryCode,omitempty"`
	PostalCode               string  `json:"postalCode,omitempty"`
}

// TableName overrides the default table name for gorm
func (EBSResponse) TableName() string {
	return "transactions"
}

type MinistatementDB []map[string]interface{}

func (m *MinistatementDB) Scan(value interface{}) error {

	b, ok := value.([]byte)
	if !ok {
		log.Printf("The type of value is: %T", value)
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(b, m)
}

// Value return json value, implement driver.Valuer interface
func (m MinistatementDB) Value() (driver.Value, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return b, nil
}

type ImportantEBSFields struct {
}

// you have to update this to account for the non-db-able fields
type EBSParserFields struct {
	EBSMapFields
	EBSResponse
	OriginalTransaction EBSResponse `json:"originalTransaction,omitempty"`
	DynamicFees         float32     `json:"dynamicFees,omitempty"`
}

// To allow Redis to use this struct directly in marshaling
func (p *EBSParserFields) MarshalBinary() ([]byte, error) {
	return json.Marshal(p)

}

// To allow Redis to use this struct directly in marshaling
func (p *EBSParserFields) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, p)
}

// special case to handle ebs non-DB-able fields e.g., hashmaps and other complex types
type EBSMapFields struct {
	// these
	Balance          map[string]interface{} `json:"balance,omitempty"`
	PaymentInfo      string                 `json:"paymentInfo,omitempty"`
	BillInfo         map[string]interface{} `json:"billInfo,omitempty"`
	LastTransactions []QRPurchase           `json:"lastTransactions,omitempty"`
}

type QRPurchase struct {
	AcqTranFee               string `json:"acqTranFee,omitempty"`
	ApplicationID            string `json:"applicationId,omitempty"`
	AuthenticationType       string `json:"authenticationType,omitempty"`
	IssuerTranFee            string `json:"issuerTranFee,omitempty"`
	MerchantAccountExpDate   string `json:"merchantAccountExpDate,omitempty"`
	MerchantAccountReference string `json:"merchantAccountReference,omitempty"`
	MerchantAccountType      string `json:"merchantAccountType,omitempty"`
	MerchantCity             string `json:"merchantCity,omitempty"`
	MerchantID               string `json:"merchantID,omitempty"`
	MerchantMobileNo         string `json:"merchantMobileNo,omitempty"`
	MerchantName             string `json:"merchantName,omitempty"`
	Pan                      string `json:"pan,omitempty"`
	ResponseCode             int64  `json:"responseCode,omitempty"`
	ResponseMessage          string `json:"responseMessage,omitempty"`
	ResponseStatus           string `json:"responseStatus,omitempty"`
	TranAmount               int64  `json:"tranAmount,omitempty"`
	TranDateTime             string `json:"tranDateTime,omitempty"`
	TranType                 string `json:"tranType,omitempty"`
	TransactionID            string `json:"transactionId,omitempty"`
	UUID                     string `json:"uuid,omitempty"`
}

type ConsumerSpecificFields struct {
	UUID            string  `json:"UUID" form:"UUID" binding:"required,len=36"`
	Ipin            string  `json:"IPIN" form:"IPIN" binding:"required"`
	PAN             string  `json:"PAN"`
	ExpDate         string  `json:"expDate"`
	PanCategory     string  `json:"panCategory"`
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

	OriginalTransactionId  string                 `json:"originalTransactionId" form:"originalTransactionId"` // for QR, sometimes
	MBR                    string                 `json:"mbr" form:"mbr"`
	PhoneNo                string                 `json:"phoneNo" form:"phoneNo"`
	NewIpin                string                 `json:"newIPIN" form:"newIPIN"`
	NewUserPassword        string                 `json:"newUserPassword" form:"newUserPassword"`
	SecurityQuestion       string                 `json:"securityQuestion" form:"securityQuestion"`
	SecurityQuestionAnswer string                 `json:"securityQuestionAnswer" form:"securityQuestionAnswer"`
	AdminUserName          string                 `json:"adminUserName" form:"adminUserName"`
	DynamicFees            float32                `json:"dynamicFees,omitempty" form:"dynamicFees"`
	TransactionID          string                 `json:"transactionId,omitempty" form:"transactionId"`
	MerchantID             string                 `json:"merchantID,omitempty" form:"merchantID"`
	AuthenticationType     string                 `json:"authenticationType,omitempty" form:"authenticationType"`
	OriginalTransaction    map[string]interface{} `json:"originalTransaction" form:"originalTransaction"`
	OriginalTranType       string                 `json:"originalTranType" form:"originalTranType"`
	FinancialInstitutionID string                 `json:"financialInstitutionId" form:"financialInstitutionId"`
}

// MaskPAN returns the last 4 digit of the PAN. We shouldn't care about the first 6
func (res *EBSResponse) MaskPAN() {
	if res.PAN != "" {
		length := len(res.PAN)
		res.PAN = res.PAN[:6] + "*****" + res.PAN[length-4:]
	}

	if res.ToCard != "" {
		length := len(res.ToCard)
		res.ToCard = res.ToCard[:6] + "*****" + res.ToCard[length-4:]
	}

	if res.FromCard != "" {
		length := len(res.FromCard)
		res.FromCard = res.FromCard[:6] + "*****" + res.FromCard[length-4:]
	}
}

type ConsumerGenerateIPINFields struct {
	Username     string `json:"userName"`
	TranDateTime string `json:"tranDateTime" binding:"required"`
	UUID         string `json:"UUID" binding:"required"`
}

type ConsumerCommonFields struct {
	ApplicationId string `json:"applicationId" form:"applicationId" binding:"required"`
	TranDateTime  string `json:"tranDateTime" form:"tranDateTime" binding:"required"`
	UUID          string `json:"UUID" form:"UUID" binding:"required"`
}

type ConsumerBillInquiryFields struct {
	ConsumerCommonFields
	ConsumersBillersFields
	ConsumerCardHolderFields
}

func (c *ConsumerBillInquiryFields) PopulateFields() {

}

type ConsumerTransactionStatusFields struct {
	ConsumerCommonFields
	OriginalTranUUID string `json:"originalTranUUID" form:"originalTranUUID" binding:"required"`
}

func (f *ConsumerBillInquiryFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerCardHolderFields struct {
	Pan     string `json:"PAN" form:"PAN" binding:"required"`
	Ipin    string `json:"IPIN" form:"IPIN" binding:"required"`
	ExpDate string `json:"expDate" form:"expDate" binding:"required"`
}

func (f *ConsumerCardHolderFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerIsAliveFields struct {
	ConsumerCommonFields
}

func (f *ConsumerIsAliveFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerBalanceFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
}

func (f *ConsumerBalanceFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumersBillersFields struct {
	PayeeId     string `json:"payeeId" form:"payeeId" binding:"required"`
	PaymentInfo string `json:"paymentInfo" form:"paymentInfo" binding:"required"`
}

func (f *ConsumersBillersFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerPurchaseFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	// PaymentDetails    []PaymentDetails `json:"paymentDetails,omitempty" form:"paymentDetails"`
	ServiceProviderId string  `json:"serviceProviderId" binding:"required"`
	DynamicFees       float32 `json:"dynamicFees,omitempty"`
}

type PaymentDetails struct {
	Account     string  `json:"account,omitempty" form:"account"`
	Amount      float64 `json:"amount,omitempty" form:"amount"`
	Description string  `json:"description,omitempty" form:"description"`
}

func (f *ConsumerPurchaseFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerQRPaymentFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	ConsumerAmountFields
	QRCode     *string `json:"QRCode,omitempty" form:"QRCode" binding:"required_without=MerchantID"`
	MerchantID *string `json:"merchantID,omitempty" binding:"required_without=QRCode"`
}

type QRMerchantFields struct {
	MerchantAccountType      string `json:"merchantAccountType" form:"merchantAccountType" binding:"required"`
	MerchantAccountReference string `json:"merchantAccountReference" form:"merchantAccountReference" binding:"required"`
	MerchantName             string `json:"merchantName" form:"merchantName" binding:"required"`
	MerchantCity             string `json:"merchantCity" form:"merchantCity" binding:"required"`
	MobileNo                 string `json:"mobileNo" form:"mobileNo" binding:"required"`
	IDType                   string `json:"idType" form:"idType" binding:"required"`
	IdNo                     string `json:"idNo" form:"idNo" binding:"required"`
	ExpDate                  string `json:"expDate" form:"expDate" binding:"required_if=MerchantAccountType CARD"`
}

type ConsumerQRRegistration struct {
	ConsumerCommonFields
	QRMerchantFields
}

type EntityFields struct {
	EntityID    string `json:"entityId"`    // starts with 249 initials
	EntityType  string `json:"entityType"`  //defaults to "Phone No"
	EntityGroup string `json:"entityGroup"` // defaults to 1
}

// ConsumerRegistrationFields the first step in card issuance
type ConsumerRegistrationFields struct {
	ConsumerCommonFields
	EntityFields
	RegistrationType string `json:"registrationType"`
	PhoneNo          string `json:"phoneNo"`
	PanCategory      string `binding:"required" json:"panCategory"`
}

type ConsumerCompleteRegistrationFields struct {
	ConsumerCommonFields
	OTP              string `json:"otp" binding:"required"`  // encrypted for fucks sake. fuck ebs
	IPIN             string `json:"IPIN" binding:"required"` // also encrypted fml forever
	ExtraInfo        string `json:"extraInfo,omitempty"`
	OriginalTranUUID string `json:"originalTranUUID" binding:"required"`
	Password         string `json:"userPassword" binding:"required"`
	// Those are noebs-specific fields and shouldn't go to EBS
	NoebsPassword string `json:"password,omitempty"`
	Mobile        string `json:"mobile,omitempty"`
}

type ConsumerGenerateVoucherFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	VoucherNumber string `json:"voucherNumber" binding:"required"`
}

func (f *ConsumerQRPaymentFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerQRRefundFields struct {
	ConsumerCommonFields
	Pan                   string `json:"PAN,omitempty" form:"PAN"`
	Ipin                  string `json:"IPIN,omitempty" form:"IPIN"`
	ExpDate               string `json:"expDate,omitempty" form:"expDate"`
	OriginalTranUUID      string `json:"originalTranUUID" binding:"required"`
	AuthenticationType    string `json:"authenticationType,omitempty"`
	OriginalTransactionId string `json:"originalTransactionId,omitempty"`
	MerchantID            string `json:"merchantID,omitempty"`
	Last4PAN              string `json:"last4PANDigits,omitempty"`
	OTP                   string `json:"OTP,omitempty"`
}

type ConsumerQRCompleteFields struct {
	ConsumerCommonFields
	OrigUUID   string `json:"originalTranUUID" binding:"required"`
	OrigTranID string `json:"originalTransactionId,omitempty"`
	OTP        string `json:"OTP,omitempty"`
}

type ConsumerQRStatus struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	Last4PAN   string `json:"last4PANDigits,omitempty"`
	MerchantID string `json:"merchantID,omitempty" binding:"required"`
	ListSize   int    `json:"listSize,omitempty"`
}

func (f *ConsumerQRRefundFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type MerchantRegistrationFields struct {
	ConsumerCommonFields
	Merchant
	//allowed fields are CARD only for now. CF ebs document
	MerchantAccountType string `json:"merchantAccountType" binding:"required"`
	// this is the pan
	MerchantAccountReference string `json:"merchantAccountReference" binding:"required"`
	ExpDate                  string `json:"expDate" binding:"required"`
}

func (f *MerchantRegistrationFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

// Merchant constructs ebs qualfied merchant
type Merchant struct {
	MerchantID           string `json:"merchant_id" form:"merchant_id" gorm:"index"`
	MerchantName         string `json:"name" form:"name" binding:"required" gorm:"column:name"`
	MerchantCity         string `json:"city" form:"city" binding:"required" gorm:"column:city"`
	MerchantMobileNumber string `json:"mobile" form:"mobile" binding:"required,max=10" gorm:"column:mobile; index:,unqiue"`
	IDType               int    `json:"id_type" form:"id_type" binding:"required" gorm:"column:id_type"`
	IDNo                 string `json:"id_no" form:"id_no" binding:"required" gorm:"column:id_no"`
	TerminalID           string `json:"-" gorm:"-"`
	PushID               string `json:"push_id" gorm:"column:push_id"`
	Password             string `json:"password"`
	IsVerifed            bool   `json:"is_verified"`
	BillerID             string `json:"biller_id"`
	EBSBiller            string `json:"ebs_biller"`
	CardNumber           string `json:"card" gorm:"column:card"`
	Hooks                string `json:"hooks" gorm:"hooks"`
	URL                  string `json:"url" gorm:"url"`
}

type mLabel struct {
	Value string
	Label string
	Help  string
}

func (m *Merchant) Details() []mLabel {
	res := []mLabel{
		{"merchantName", "Merchant Name", "Enter merchant name"},
		{"mobileNo", "Mobile Number", "Enter mobile number"},
		{"merchantCity", "Merchant City", "Enter merchant city"},
		{"idType", "ID Type (National ID, Driving License", "Enter ID Type"},
		{"idNo", "ID number", "Enter id no"},
	}
	return res
}

func (m *Merchant) ToMap() map[string]interface{} {
	a := make(map[string]interface{})
	a["merchantName"] = m.MerchantName
	a["mobileNo"] = m.MerchantMobileNumber
	a["merchantCity"] = m.MerchantCity
	a["idType"] = m.IDType
	a["idNo"] = m.IDNo
	return a
}

func (m *Merchant) MarshalBinary() ([]byte, error) {
	return json.Marshal(m)
}

type ConsumerBillPaymentFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	ConsumersBillersFields
}

func (f *ConsumerBillPaymentFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerWorkingKeyFields struct {
	ConsumerCommonFields
}

func (f *ConsumerWorkingKeyFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerIPinFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	NewIPIN string `json:"newIPIN" binding:"required"`
}

func (f *ConsumerIPinFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerCardTransferAndMobileFields struct {
	ConsumerCardTransferFields
	Mobile string `json:"mobile_number"`
}

type ConsumerCashInFields struct {
	ConsumerCardTransferFields
}

type ConsumerCashoOutFields struct {
	ConsumerCardTransferFields
}

type ConsumerCardTransferFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	ToCard      string  `json:"toCard" binding:"required"`
	DynamicFees float32 `json:"dynamicFees,omitempty"`
}

type ConsumrAccountTransferFields struct {
	ConsumerCommonFields
	ConsumerCardHolderFields
	AmountFields
	ToAccount string `json:"toAccount" binding:"required"`
}

// MustMarshal panics if not able to marshal repsonse
func (p2p *ConsumerCardTransferFields) MustMarshal() []byte {
	d, _ := json.Marshal(p2p)
	return d
}

type ConsumerStatusFields struct {
	ConsumerCommonFields
	OriginalTranUUID string `json:"originalTranUUID" binding:"required"`
}

func (f *ConsumerStatusFields) MustMarshal() []byte {
	d, _ := json.Marshal(f)
	return d
}

type ConsumerGenerateIPin struct {
	ConsumerGenerateIPINFields
	Password     string `json:"password"`
	Pan          string `json:"pan"`
	MobileNumber string `json:"phoneNumber" binding:"required"`
	Expdate      string `json:"expDate"`
}

func (gi *ConsumerGenerateIPin) MustMarshal() []byte {
	d, _ := json.Marshal(gi)
	return d
}

type ConsumerGenerateIPinCompletion struct {
	ConsumerGenerateIPINFields
	Password string `json:"password" binding:"required"`
	Pan      string `json:"pan" binding:"required"`
	Expdate  string `json:"expDate" binding:"required"`
	Otp      string `json:"otp"  binding:"required"`
	Ipin     string `json:"ipin" binding:"required"`
}

type ConsumerPANFromMobileFields struct {
	ConsumerCommonFields
	EntityID string `json:"entityId" binding:"required"`
	Last4PAN string `json:"last4PANDigits" binding:"required"`
}

type ConsumerCardInfoFields struct {
	ConsumerCommonFields
	PAN string `json:"PAN" binding:"required"`
}

func (gip *ConsumerGenerateIPinCompletion) MustMarshal() []byte {
	d, _ := json.Marshal(gip)
	return d
}

type DisputeFields struct {
	Time    string  `json:"time,omitempty"`
	Service string  `json:"service,omitempty"`
	UUID    string  `json:"uuid,omitempty"`
	STAN    int     `json:"stan,omitempty"`
	Amount  float32 `json:"amount,omitempty"`
}

func (d *DisputeFields) New(f EBSParserFields) *DisputeFields {
	d.Amount = f.TranAmount
	d.Service = f.EBSServiceName
	d.UUID = f.UUID
	d.Time = f.TranDateTime
	return d
}

type MobileRedis struct {
	Mobile   string `json:"mobile" binding:"required"`
	Provider string `json:"provider"`
	IsMain   bool   `json:"is_main"`
}

type ItemID struct {
	ID     int  `json:"id,omitempty" binding:"required"`
	IsMain bool `json:"is_main"`
}

func isEBS(pan string) bool {
	/*
		Bank Code        Bank Card PREFIX        Bank Short Name        Bank Full name
		2                    639186                      FISB                                 Faisal Islamic Bank
		4                    639256                      BAKH                                  Bank of Khartoum
		16                    639184                       RAKA                                  Al Baraka Sudanese Bank
		30                    639330                       ALSA                                  Al Salam Bank
	*/

	re := regexp.MustCompile(`(^639186|^639256|^639184|^639330)`)
	return re.Match([]byte(pan))
}

type TokenCard struct {
	CardInfoFields
	Fingerprint string `json:"fingerprint" binding:"required"`
}

type ValidationError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// NoebsConfig contains all about noebs configuration, including ebs ips and ports,
// redis ips, and so on.
// The file currently reads from `ebs_fields/.secrets.json` using go embedding fs.
// NoebsConfig can be accessed via [NoebsSecrets] which is initialized in the
// [ebs_fields] package in the init method.
type NoebsConfig struct {
	OneSignal  string `json:"onesignal_key"`
	SMSAPIKey  string `json:"sms_key"`
	SMSSender  string `json:"sms_sender"`
	SMSGateway string `json:"sms_gateway"`
	RedisPort  string `json:"redis_port"`
	JWTKey     string `json:"jwt_secret"`
	Sentry     string `json:"sentry"`
	Port       string `json:"port"`

	IsConsumerProd bool `json:"is_consumer_prod"`
	IsMerchantProd bool `json:"is_merchant_prod"`

	ConsumerQAIP string `json:"consumer_qa"`
	MerchantQAIP string `json:"merchant_qa"`
	ConsumerProd string `json:"consumer_prod"`
	MerchantProd string `json:"merchant_prod"`

	ConsumerIP string
	MerchantIP string
	IPINIp     string

	// Those should be depricated they are confusing with no good use case
	IPIN   string `json:"ipin_prod"`
	IPINQA string `json:"ipin_qa"`

	ConsumerID string `json:"-"` // The ID that will be used within noebs to identify the consumer
	MerchantID string `json:"-"` // The ID that will be used within noebs to identify the merchant

	ConsumerQAID string `json:"consumer_qa_id"` // EBS application ID for mobile and card not present services prod)
	MerchantQAID string `json:"merchant_qa_id"` // EBS client ID for pos and merchant services (prod)

	ConsumerProdID string `json:"consumer_prod_id"` // EBS application ID for mobile and card not present services prod)
	MerchantProdID string `json:"merchant_prod_id"` // EBS client ID for pos and merchant services (prod)

	// IPIN generation configs
	EBSIPINUsername string `json:"ipin_username"`
	EBSIPINPassword string `json:"ipin_password"`

	// Bill inquiry specific hard-coded card holder information
	// We highly don't recommend to store data, but this can be an exception
	BillInquiryPAN     string `json:"pan"`
	BillInquiryPIN     string `json:"pin"`
	BillInquiryIPIN    string `json:"ipin"`
	BillInquiryExpDate string `json:"exp_date"`
	EBSConsumerKey     string `json:"pub_key"`

	// server config
	Cors    []string `json:"cors"`
	IsDebug bool     `json:"is_debug"` // set as true if you want to have more debug options
}

func (n *NoebsConfig) Defaults() {
	if n.IsConsumerProd {
		n.ConsumerIP = n.ConsumerProd
		n.IPINIp = n.IPIN
		n.ConsumerID = n.ConsumerProdID
	} else {
		n.ConsumerIP = n.ConsumerQAIP
		n.IPINIp = n.IPINQA
		n.ConsumerID = n.ConsumerQAID
	}
	if n.IsMerchantProd {
		n.MerchantIP = n.MerchantProd
		n.MerchantID = n.MerchantProdID
	} else {
		n.MerchantIP = n.MerchantQAIP
		n.MerchantID = n.MerchantQAID
	}
}

type QuickPaymentFields struct {
	EncodedPaymentToken string `json:"token"`
	ConsumerCardTransferFields
}

func (q QuickPaymentFields) MarshallP2pFields() []byte {
	d := ConsumerCardTransferFields{
		ConsumerCommonFields:     q.ConsumerCommonFields,
		AmountFields:             q.AmountFields,
		ConsumerCardHolderFields: q.ConsumerCardHolderFields,
		ToCard:                   q.ToCard, // must be acquired from the generated card.
	}
	data, _ := json.Marshal(&d)
	return data
}

type CacheBillers struct {
	Mobile   string `gorm:"primaryKey"`
	BillerID string
}

func (c *CacheBillers) Save(db *gorm.DB, flipBiller bool) error {
	newId := c.BillerID
	if flipBiller {
		switch c.BillerID {
		case "0010010002": // zain bill payment
			newId = "0010010001" // zain top up
		case "0010010001":
			newId = "0010010002"

		case "0010010004": // mtn bill payment
			newId = "0010010003" // mtn top up
		case "0010010003":
			newId = "0010010004"

		case "0010010006": // sudani bill payment
			newId = "0010010005" // sudani top up
		case "0010010005":
			newId = "0010010006"
		}
		c.BillerID = newId
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "mobile"}}, DoUpdates: clause.Assignments(map[string]any{"biller_id": c.BillerID}),
	}).Create(&c).Error
}

func GetBillerInfo(mobile string, db *gorm.DB) (CacheBillers, error) {
	c := CacheBillers{Mobile: mobile}
	res := db.First(&c)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return c, res.Error
	}
	return c, nil
}

func UpdateBiller(mobile, biller string, db *gorm.DB) (CacheBillers, error) {
	c := CacheBillers{Mobile: mobile}
	res := db.Model(&c).Where("mobile = ?", mobile).Update("biller_id", biller)
	if res.Error != nil {
		return c, res.Error
	}
	return c, nil
}

// Saves or updates any type that implements noebsdatabase. Currently it works best when the case is to only update one field (flag in db e.g., is_valid)
// In case of no conflicts, it writes to db directly
func SaveOrUpdates(db *gorm.DB, entity NoebsDatabase, newVal any) error {
	card := entity.(CacheCards)
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: entity.GetPk()}}, DoUpdates: clause.Assignments(map[string]any{entity.OverrideField(): newVal}),
	}).Create(&card).Error
}
