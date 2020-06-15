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

const EBSMerchantIPTesting = "https://172.16.199.1:8181/QAEBSGateway/"
const EBSMerchantIP = "https://172.16.198.14:8888/EBSGateway/"

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
	EBSIpConsumerTesting = "https://172.16.199.1:8877/QAConsumer/"
	EBSIp                = "https://172.24.160.30:8443/Consumer/"
)

const (
	ConsumerIsAliveEndpoint      = "isAlive"
	ConsumerWorkingKeyEndpoint   = "getPublicKey"
	ConsumerBalanceEndpoint      = "getBalance"
	ConsumerBillInquiryEndpoint  = "getBill"
	ConsumerBillPaymentEndpoint  = "payment"
	ConsumerCardTransferEndpoint = "doCardTransfer"
	ConsumerPayeesListEndpoint   = "getPayeesList"
	ConsumerChangeIPinEndpoint   = "changeIPin"
	ConsumerPurchaseEndpoint     = "specialPayment"
	ConsumerStatusEndpoint       = "getTransactionStatus"
	ConsumerQRPaymentEndpoint    = "doQRPurchase"
	ConsumerQRGenerationEndpoint = "doMerchantRegistration"
	ConsumerQRRefundEndpoint     = "doQRRefund"
	ConsumerPANFromMobile        = "checkMsisdnAganistPAN"
	ConsumerCardInfo             = "getCustomerInfo"

	// IPIN generation
	IPinGeneration = "doGenerateIPinRequest"
	IPinCompletion = "doGenerateCompletionIPinRequest"
)
