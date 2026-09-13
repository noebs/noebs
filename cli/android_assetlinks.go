package main

import "github.com/gofiber/fiber/v2"

// These are the established Android signing identities for TutiPay alpha.
const androidAssetLinks = `[{"relation":["delegate_permission/common.handle_all_urls"],"target":{"namespace":"android_app","package_name":"com.tutipay.app.alpha","sha256_cert_fingerprints":["B4:45:C2:79:FE:FB:B0:95:AA:33:4F:67:42:4D:EA:6B:52:77:38:EA:FF:A5:EF:FB:80:B5:E2:F5:9B:66:1C:AE","BB:1F:DB:0B:76:AE:89:D6:B6:BD:7A:D4:A1:5F:85:59:60:16:55:48:73:8E:E4:B8:DC:89:4A:8F:BC:1A:AE:F5","08:16:D2:E0:36:91:A9:FB:4C:39:A4:49:DB:18:B6:FD:DB:35:7C:52:83:96:1F:1A:19:34:D7:6D:2A:99:0A:A7"]}}]`

func registerAndroidAssetLinksRoute(route *fiber.App, role serviceRole) {
	if role != serviceRoleAPIGateway {
		return
	}
	route.Get("/.well-known/assetlinks.json", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		c.Set(fiber.HeaderCacheControl, "public, max-age=3600")
		return c.SendString(androidAssetLinks)
	})
}
