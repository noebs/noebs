package handler

import "github.com/adonese/noebs/consumer"

// Handler implements the HTTP boundary for consumer APIs.
//
// Keep this layer thin: bind/validate, map to domain types, and call the consumer service.
type Handler struct {
	Service *consumer.Service
}

func New(service *consumer.Service) (*Handler, error) {
	if service == nil {
		return nil, consumer.ErrMissingService
	}
	if service.Store == nil {
		return nil, consumer.ErrMissingStore
	}
	return &Handler{Service: service}, nil
}
