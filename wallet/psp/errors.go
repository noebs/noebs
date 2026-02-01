package psp

import "errors"

var (
	ErrPSPNotRegistered  = errors.New("psp not registered")
	ErrPSPSecretMissing  = errors.New("psp secret missing")
	ErrPSPConfigInvalid  = errors.New("psp config invalid")
	ErrPSPWebhookInvalid = errors.New("psp webhook invalid")
	ErrPSPTemporary      = errors.New("psp temporary error")
	ErrPSPPermanent      = errors.New("psp permanent error")
)
