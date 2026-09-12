package main

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

type accountService struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Action    string `json:"action"`
}

func accountServices(cfg ebs_fields.NoebsConfig) []accountService {
	return []accountService{
		{ID: "send_money", Available: cfg.WalletEnabled, Action: "send_money"},
		{ID: "add_money", Available: cfg.WalletEnabled, Action: "add_money"},
		{ID: "activity", Available: cfg.WalletEnabled, Action: "activity"},
		{ID: "identity", Available: true, Action: "identity"},
		{ID: "profile", Available: true, Action: "profile"},
		{ID: "cards", Available: cfg.OpaqueCardManagementEnabled, Action: "cards"},
		{ID: "chat", Available: cfg.ChatEnabled, Action: "chat"},
	}
}

func accountServicesHandler(cfg ebs_fields.NoebsConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		return c.JSON(fiber.Map{"services": accountServices(cfg)})
	}
}
