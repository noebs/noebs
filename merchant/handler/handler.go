package handler

import "github.com/adonese/noebs/merchant"

// Handler implements the HTTP boundary for merchant APIs.
//
// Keep this layer thin: bind/validate, apply config defaults, map to domain types,
// and call the merchant service.
type Handler struct {
	Service *merchant.Service
}

func New(service *merchant.Service) *Handler {
	return &Handler{Service: service}
}

