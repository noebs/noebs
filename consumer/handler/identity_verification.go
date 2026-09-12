package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
)

func (h *Handler) GetIdentityVerification(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	result, err := h.Service.GetIdentityVerification(c.UserContext(), owner)
	if errors.Is(err, sql.ErrNoRows) {
		return fiber.NewError(http.StatusNotFound, "account verification not found")
	}
	if err != nil {
		return fiber.NewError(http.StatusInternalServerError, "account verification unavailable")
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(result)
}
