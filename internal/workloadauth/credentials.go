package workloadauth

import (
	"net/http"
	"strings"
)

// StripCredentials removes Noebs workload and asserted identity credentials
// from a request before it is repurposed for another destination. It leaves the
// request ID and ordinary representation headers intact.
func StripCredentials(req *http.Request) {
	if req == nil || req.Header == nil {
		return
	}
	for name := range req.Header {
		if isCredentialHeader(name) {
			delete(req.Header, name)
		}
	}
}

// RejectRedirect is suitable for http.Client.CheckRedirect. Signed internal
// requests should not forward custom identity headers to a redirect target.
func RejectRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func isCredentialHeader(name string) bool {
	for _, candidate := range workloadHeaders {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	for _, candidate := range identityHeaders {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	return false
}
