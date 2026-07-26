package handler

import (
	"net/http"
	"strings"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func (h *Handler) StoreNotificationPushData(c *fiber.Ctx) error {
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var cmd consumer.StorePushDataCommand
	if err := parseJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	if err := normalizeStorePushDataCommand(&cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	if err := h.Service.StoreNotificationPushData(c.UserContext(), tenantID, cmd); err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return c.SendStatus(http.StatusNoContent)
}

func normalizeStorePushDataCommand(cmd *consumer.StorePushDataCommand) error {
	data := &cmd.Data
	data.UUID = strings.TrimSpace(data.UUID)
	data.To = strings.TrimSpace(data.To)
	data.Phone = strings.TrimSpace(data.Phone)
	data.DeviceID = strings.TrimSpace(data.DeviceID)
	data.UserMobile = strings.TrimSpace(data.UserMobile)
	data.TransactionUUID = strings.TrimSpace(data.TransactionUUID)
	if data.TransactionUUID == "" {
		return nil
	}
	transactionUUID, err := uuid.Parse(data.TransactionUUID)
	if err != nil || transactionUUID.String() != data.TransactionUUID {
		return store.ErrInvalidTransactionUUID
	}
	data.EBSUUID = data.TransactionUUID
	return nil
}
