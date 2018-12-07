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
	ToCard string `binding:"toCard" binding:"required"`
}

type PurchaseFields struct {
	WorkingKeyFields
	CardInfoFields
	AmountFields
}

type ChangePin struct {
	WorkingKeyFields
	NewPin string `json:"newPIN" binding:"required"`
}

type CommonFields struct {
	SystemTraceAuditNumber int       `json:"systemTraceAuditNumber" binding:"required"`
	TranDateTime           time.Time `json:"tranDateTime" binding:"required"`
	TerminalID             string    `json:"terminalId" binding:"required,len=8"`
	ClientID               string    `json:"clientId" binding:"required"`
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
