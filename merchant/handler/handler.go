package handler

import "github.com/adonese/noebs/merchant"

// Handler implements the HTTP boundary for merchant APIs.
//
// Keep this layer thin: bind/validate, map to domain types, and call the merchant service.
type Handler struct {
	Service *merchant.Service
}

func New(service *merchant.Service) (*Handler, error) {
	if service == nil {
		return nil, merchant.ErrMissingService
	}
	if service.Store == nil {
		return nil, merchant.ErrMissingStore
	}
	return &Handler{Service: service}, nil
}
