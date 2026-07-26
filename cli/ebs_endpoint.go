package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var errInvalidEBSEndpoint = errors.New("invalid EBS endpoint")

func validateEBSEndpoint(label, value string) error {
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%w: %s must not contain surrounding whitespace", errInvalidEBSEndpoint, label)
	}
	endpoint, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%w: parse %s: %w", errInvalidEBSEndpoint, label, err)
	}
	if endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.Opaque != "" {
		return fmt.Errorf("%w: %s must be an absolute HTTPS URL", errInvalidEBSEndpoint, label)
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return fmt.Errorf("%w: %s must not contain credentials, query, or fragment", errInvalidEBSEndpoint, label)
	}
	return nil
}
