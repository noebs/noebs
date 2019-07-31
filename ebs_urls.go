package main

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

const EBSMerchantIP = "https://172.16.199.1:8181/QAEBSGateway/"

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
	EBSIp = "https://172.16.199.1:8877/QAEBSGateway/"
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
)
