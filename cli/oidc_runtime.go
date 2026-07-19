package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/oidcauth"
)

const oidcHTTPTimeout = 10 * time.Second

var oidcVerifier *oidcauth.Verifier

func initOIDCVerifier(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	oidcVerifier = nil
	if role != serviceRoleAPIGateway {
		return nil
	}
	client := httpclient.New(
		httpclient.WithTimeout(oidcHTTPTimeout),
		httpclient.WithResponseHeaderTimeout(5*time.Second),
	)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	verifier, err := oidcauth.NewRemoteVerifier(cfg.OIDC, client, oidcauth.SystemClock{})
	if err != nil {
		return fmt.Errorf("noebs.oidc: %w", err)
	}
	oidcVerifier = verifier
	return nil
}
