package main

import "time"

// not sure this would work. This package is just for storing struct representations
// of each httpHandler

type WorkingKeyFields struct {
	CommonFields
}

type CardTransferFields struct {
	CommonFields
	CardInfoFields
	AmountFields
	ToCard string `validator:"toCard" binding:"required"`
}

type PurchaseFields struct {
	WorkingKeyFields
	CardInfoFields
	AmountFields
}

type ChangePin struct {
	WorkingKeyFields
	NewPin string `validator:"newPIN" binding:"required"`
}

type CommonFields struct {
	SystemTraceAuditNumber int       `validator:"systemTraceAuditNumber" binding:"required"`
	TranDateTime           time.Time `validator:"tranDateTime" binding:"required"`
	TerminalID             string    `validator:"terminalId" binding:"required,len=8"`
	ClientID               string    `validator:"clientId" binding:"required"`
}

type CardInfoFields struct {
	Pan     string `validator:"PAN" binding:"required"`
	Pin     string `validator:"PIN" binding:"required"`
	Expdate string `validator:"expDate" binding:"required"`
}

type AmountFields struct {
	TranAmount       float32 `validator:"tranAmount" binding:"required"`
	TranCurrencyCode string  `validator:"tranCurrencyCode"`
}
