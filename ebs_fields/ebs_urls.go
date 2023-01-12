package ebs_fields

const (
	IsAliveEndpoint                  = "isAlive"
	PurchaseEndpoint                 = "purchase"
	PurchaseWithCashBackEndpoint     = "purchaseWithCashBack"
	PurchaseMobileEndpoint           = "purchaseMobile"
	ReverseEndpoint                  = "reverse"
	BalanceEndpoint                  = "getBalance"
	MiniStatementEndpoint            = "getMiniStatement"
	RefundEndpoint                   = "refund"
	BillInquiryEndpoint              = "getBill"
	BillPaymentEndpoint              = "payBill"
	BillPrepaymentEndpoint           = "prepayBill"
	AccountTransferEndpoint          = "doAccountTransfer"
	CardTransferEndpoint             = "doCardTransfer"
	NetworkTestEndpoint              = "isAlive"
	WorkingKeyEndpoint               = "getWorkingKey"
	PayeesListEndpoint               = "getPayeesList"
	CashInEndpoint                   = "cashIn"
	CashOutEndpoint                  = "cashOut"
	GenerateVoucherEndpoint          = "generateVoucher"
	VoucherCashOutWithAmountEndpoint = "cashOutVoucher"
	VoucherCashInEndpoint            = "voucherCashIn"
	GenerateOTPEndpoint              = "generateOTP"
	ChangePINEndpoint                = "changePin"
)

const (
	PurchaseTransaction             = "PurchaseTransaction"
	PurchaseWithCashBackTransaction = "PurchaseWithCashBack"
	BillPaymentTransaction          = "BillPayment"
	BillInquiryTransaction          = "BillInquiry"
	CardTransferTransaction         = "CardTransfer"
	WorkingKeyTransaction           = "WorkingKeyFields"
	ChangePINTransaction            = "ChangePINTransaction"
	RefundTransaction               = "RefundTransaction"
	CashInTransaction               = "CashInTransaction"
	CashOutTransaction              = "CashOutTransaction"
	MiniStatementTransaction        = "MiniStatementTransaction"
	IsAliveTransaction              = "IsAliveTransaction"
	BalanceTransaction              = "BalanceTransaction"
	PayeesListTransaction           = "PayeesListTransaction"
)

const (
	ConsumerIsAliveEndpoint           = "isAlive"
	ConsumerWorkingKeyEndpoint        = "getPublicKey"
	ConsumerBalanceEndpoint           = "getBalance"
	ConsumerBillInquiryEndpoint       = "getBill"
	ConsumerBillPaymentEndpoint       = "payment"
	ConsumerCardTransferEndpoint      = "doCardTransfer"
	ConsumerAccountTransferEndpoint   = "doAccountTransfer"
	ConsumerPayeesListEndpoint        = "getPayeesList"
	ConsumerChangeIPinEndpoint        = "changeIPin"
	ConsumerPurchaseEndpoint          = "specialPayment"
	ConsumerStatusEndpoint            = "getTransactionStatus"
	ConsumerQRPaymentEndpoint         = "doQRPurchase"
	ConsumerQRGenerationEndpoint      = "doMerchantsRegistration" // the fuck is wrong with you guys
	ConsumerQRRefundEndpoint          = "doQRRefund"
	ConsumerPANFromMobile             = "checkMsisdnAganistPAN"
	ConsumerCardInfo                  = "getCustomerInfo"
	ConsumerGenerateVoucher           = "generateVoucher"
	ConsumerCashInEndpoint            = "doCashIn"
	ConsumerCashOutEndpoint           = "doCashOut"
	ConsumerTransactionStatusEndpoint = "getTransactionStatus"
	ConsumerComplete                  = "completeTransaction"

	// IPIN generation
	IPinGeneration            = "doGenerateIPinRequest"
	IPinCompletion            = "doGenerateCompletionIPinRequest"
	QRPublicKey               = "getPublicKey"
	MerchantTransactionStatus = "getMerchantTransactions"

	ConsumerRegister             = "register"
	ConsumerCompleteRegistration = "completeCardRegistration"
)

// DynamicFeesFields for p2p and mohe dynamic fees case
type DynamicFeesFields struct {
	CardTransferfees   float32 `json:"p2p_fees"`
	CustomFees         float32 `json:"custom_fees"`
	SpecialPaymentFees float32 `json:"special_payment_fees"`
}

func NewDynamicFeesWithDefaults() DynamicFeesFields {
	return DynamicFeesFields{
		// dynamic fees are set to the minimum amount required by ebs
		//whih is as follows:
		//p2p [7.5-150]
		//customs [85-150]
		//special payment [2-150]
		CardTransferfees:   7.5, // ebs QA server returns error for 1 fees
		SpecialPaymentFees: 2,
		CustomFees:         85, //TODO add custom fees to the request
	}
}
