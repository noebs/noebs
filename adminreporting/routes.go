package adminreporting

import (
	"encoding/json"
	"errors"
	"net/http"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func RegisterInternalRoutes(router fiber.Router, service *Service) {
	router.Post("/transactions", service.StoreTransactionProjectionHandler)
}

func (s *Service) StoreTransactionProjectionHandler(c *fiber.Ctx) error {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	var cmd TransactionProjectionCommand
	if err := parseJSON(c, &cmd); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	if err := s.StoreTransactionProjection(c.UserContext(), tenantID, cmd); err != nil {
		return c.Status(statusForError(err)).JSON(fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return c.SendStatus(http.StatusNoContent)
}

func parseJSON(c *fiber.Ctx, dst any) error {
	if len(c.Body()) == 0 {
		return ErrMissingTransactionProjection
	}
	return json.Unmarshal(c.Body(), dst)
}

func resolveTenantID(c *fiber.Ctx) (string, error) {
	if v := c.Locals("tenant_id"); v != nil {
		if tenantID, ok := v.(string); ok && tenantID != "" {
			return store.ValidateTenantID(tenantID)
		}
	}
	return store.ValidateTenantID(c.Get(gateway.GatewayTenantIDHeader))
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, store.ErrMissingTenantID),
		errors.Is(err, store.ErrInvalidTenantID),
		errors.Is(err, ErrMissingTransactionProjection):
		return http.StatusBadRequest
	case errors.Is(err, ErrMissingStore):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
