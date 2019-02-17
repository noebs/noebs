package main

const (
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

const EBSMerchantIP = "https://197.16.8.8:8080/EBSGateway/"

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
	MiniStatementTransaction = "MiniStatementTransaction"
)
