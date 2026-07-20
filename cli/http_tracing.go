package main

import (
	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
)

func httpTracingMiddleware(serverName string, additional ...otelfiber.Option) fiber.Handler {
	options := []otelfiber.Option{
		otelfiber.WithServerName(serverName),
		otelfiber.WithNext(skipSensitiveAuthLifecycleTrace),
		otelfiber.WithSpanNameFormatter(func(ctx *fiber.Ctx) string {
			if route := ctx.Route(); route != nil && route.Path != "" {
				return ctx.Method() + " " + route.Path
			}
			return ctx.Method() + " " + ctx.Path()
		}),
	}
	return otelfiber.Middleware(append(options, additional...)...)
}

func skipSensitiveAuthLifecycleTrace(ctx *fiber.Ctx) bool {
	switch ctx.Path() {
	case backofficeCallbackPath,
		walletAuthorizationBrowserStartPath,
		walletAuthorizationCallbackPath:
		return true
	default:
		return false
	}
}
