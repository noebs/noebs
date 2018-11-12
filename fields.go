package main

import "time"

// not sure this would work. This package is just for storing struct representations
// of each httpHandler

type CardTransferFields struct {
	SystemTraceAuditNumber int       `validator:"systemTraceAuditNumber" binding:"required"`
	TranDateTime           time.Time `validator:"tranDateTime" binding:"required"`
	TerminalID             string    `validator:"terminalId" binding:"required,len=8"`
	ClientID               string    `validator:"clientId" binding:"required"`
	TranCurrencyCode       string    `validator:"tranCurrencyCode"`
	Pan                    string    `validator:"PAN" binding:"required"`
	Pin                    string    `validator:"PIN" binding:"required"`
	Expdate                string    `validator:"expDate" binding:"required"`
	TranAmount             float32   `validator:"tranAmount" binding:"required"`
	ToCard                 string    `validator:"toCard" binding:"required"`
}

type PurchaseFields struct {
	SystemTraceAuditNumber int       `validator:"systemTraceAuditNumber" binding:"required"`
	TranDateTime           time.Time `validator:"tranDateTime" binding:"required"`
	TerminalID             string    `validator:"terminalId" binding:"required,len=8"`
	ClientID               string    `validator:"clientId" binding:"required"`
	TranCurrencyCode       string    `validator:"tranCurrencyCode"`
	Pan                    string    `validator:"PAN" binding:"required"`
	Pin                    string    `validator:"PIN" binding:"required"`
	Expdate                string    `validator:"expDate" binding:"required"`
	TranAmount             float32   `validator:"tranAmount" binding:"required"`
}

type ChangePin struct {
	SystemTraceAuditNumber int       `validator:"systemTraceAuditNumber" binding:"required"`
	TranDateTime           time.Time `validator:"tranDateTime" binding:"required"`
	TerminalID             string    `validator:"terminalId" binding:"required,len=8"`
	ClientID               string    `validator:"clientId" binding:"required"`
	Pan                    string    `validator:"PAN" binding:"required"`
	Pin                    string    `validator:"PIN" binding:"required"`
	Expdate                string    `validator:"expDate" binding:"required"`
	NewPin                 string    `validator:"newPIN" binding:"required"`
}

type WorkingKeyFields struct {
	SystemTraceAuditNumber int       `validator:"systemTraceAuditNumber" binding:"required"`
	TranDateTime           time.Time `validator:"tranDateTime" binding:"required"`
	TerminalID             string    `validator:"terminalId" binding:"required,len=8"`
	ClientID               string    `validator:"clientId" binding:"required"`
}
