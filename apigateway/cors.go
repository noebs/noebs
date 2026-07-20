package gateway

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func NoebsCors(origins []string) fiber.Handler {
	if len(origins) == 0 {
		return func(c *fiber.Ctx) error { return c.Next() }
	}
	return cors.New(cors.Config{
		AllowOrigins: strings.Join(origins, ","),
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders: "Authorization,Origin,Content-Type,Accept,X-CSRF-Token,X-Active-Tenant",
		MaxAge:       600,
	})
}
