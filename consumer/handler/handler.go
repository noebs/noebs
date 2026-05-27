package handler

import "github.com/adonese/noebs/consumer"

// Handler implements the HTTP boundary for consumer APIs.
//
// Keep this layer thin: bind/validate, map to domain types, and call the consumer service.
type Handler struct {
	Service *consumer.Service
}

func New(service *consumer.Service) *Handler {
	return &Handler{Service: service}
}
