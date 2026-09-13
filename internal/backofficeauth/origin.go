package backofficeauth

import (
	"net"
	"net/url"
	"regexp"
)

var originDNSName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidateSeparateOrigin requires one exact browser origin independently of the
// public application. Network deployment owns its private Tailscale exposure.
func ValidateSeparateOrigin(origin, publicOrigin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host == "" ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		len(u.Host) > 253 || !originDNSName.MatchString(u.Host) || net.ParseIP(u.Host) != nil ||
		u.String() != origin || publicOrigin == "" || origin == publicOrigin {
		return ErrInvalidConfiguration
	}
	return nil
}
