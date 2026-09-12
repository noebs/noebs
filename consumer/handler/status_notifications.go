package handler

import (
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v2"
)

func (h *Handler) StatusNotifications(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return fiber.NewError(http.StatusBadRequest, "limit must be between 1 and 100")
		}
		limit = parsed
	}
	events, err := h.Service.Store.ListStatusNotifications(c.UserContext(), owner.TenantID, owner.UserID, limit)
	if err != nil {
		return fiber.NewError(http.StatusInternalServerError, "notifications unavailable")
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"events": events})
}
