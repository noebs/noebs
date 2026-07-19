package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

const workloadPrincipalLocal = "workload_principal"

var (
	workloadSigners      *workloadauth.SignerSet
	workloadVerifier     workloadRequestVerifier
	workloadAuthDatabase *store.DB
)

type workloadRequestVerifier interface {
	Verify(*http.Request, []byte) (workloadauth.Principal, error)
}

type workloadCapability struct {
	caller string
	method string
	path   string
}

func roleReceivesSignedHTTP(role serviceRole) bool {
	return role.startsHTTP() && role != serviceRoleAPIGateway
}

func workloadCallerAudiences(role serviceRole) []string {
	switch role {
	case serviceRoleAPIGateway:
		return []string{
			string(serviceRoleIdentityAuth),
			string(serviceRoleCardVault),
			string(serviceRoleEBSAdapter),
			string(serviceRolePSPWebhook),
			string(serviceRoleAdminReporting),
			string(serviceRoleNotification),
			string(serviceRoleBeneficiary),
			string(serviceRoleWalletAPI),
		}
	case serviceRoleIdentityAuth:
		return []string{string(serviceRoleCardVault)}
	case serviceRoleEBSAdapter:
		return []string{
			string(serviceRoleIdentityAuth),
			string(serviceRoleCardVault),
			string(serviceRoleNotification),
		}
	case serviceRoleNotification:
		return []string{string(serviceRoleIdentityAuth)}
	default:
		return nil
	}
}

func expectedWorkloadCallers(role serviceRole) map[string]bool {
	callers := map[string]bool{"api-gateway": true}
	switch role {
	case serviceRoleIdentityAuth:
		callers["ebs-adapter"] = true
		callers["notification-chat"] = true
	case serviceRoleCardVault:
		callers["identity-auth"] = true
		callers["ebs-adapter"] = true
	case serviceRoleNotification:
		callers["ebs-adapter"] = true
	}
	return callers
}

func validateWorkloadAuthRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	audiences := workloadCallerAudiences(role)
	_, _, signingKeyPresent, signingErr := cfg.WorkloadAuth.SigningKey()
	if signingErr != nil {
		return fmt.Errorf("noebs.workload_auth signing key: %w", signingErr)
	}
	if len(audiences) > 0 && !signingKeyPresent {
		return errors.New("noebs.workload_auth signing key is required")
	}
	if len(audiences) == 0 && signingKeyPresent {
		return errors.New("noebs.workload_auth signing key is not allowed for this role")
	}

	registry, err := cfg.WorkloadAuth.Registry()
	if err != nil {
		return fmt.Errorf("noebs.workload_auth trusted_keys: %w", err)
	}
	if !roleReceivesSignedHTTP(role) {
		if len(registry) != 0 || strings.TrimSpace(cfg.WorkloadAuth.NonceDatabaseURL) != "" {
			return errors.New("noebs.workload_auth receiver config is not allowed for this role")
		}
		return nil
	}
	if strings.TrimSpace(cfg.WorkloadAuth.NonceDatabaseURL) == "" {
		return errors.New("noebs.workload_auth.nonce_db_url is required")
	}
	if len(registry) == 0 {
		return errors.New("noebs.workload_auth.trusted_keys is required")
	}
	expected := expectedWorkloadCallers(role)
	seen := make(map[string]bool, len(expected))
	for _, registered := range registry {
		if !expected[registered.Caller] {
			return fmt.Errorf("noebs.workload_auth caller %q is not authorized for %s", registered.Caller, role)
		}
		seen[registered.Caller] = true
	}
	for caller := range expected {
		if !seen[caller] {
			return fmt.Errorf("noebs.workload_auth missing trusted key for %s", caller)
		}
	}
	return nil
}

func initWorkloadAuth(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	workloadSigners = nil
	workloadVerifier = nil
	workloadAuthDatabase = nil

	if audiences := workloadCallerAudiences(role); len(audiences) > 0 {
		signers, err := workloadauth.NewSignerSet(cfg.WorkloadAuth, audiences)
		if err != nil {
			return fmt.Errorf("configure workload signer: %w", err)
		}
		workloadSigners = signers
	}
	if !roleReceivesSignedHTTP(role) {
		return nil
	}

	db, err := store.OpenFromConfigWithCACertificate(cfg.WorkloadAuth.NonceDatabaseURL, store.DriverPostgres, cfg.DatabaseCACertificate)
	if err != nil {
		return fmt.Errorf("open workload nonce database: %w", err)
	}
	nonces, err := workloadauth.NewPostgresNonceStore(db.DB)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("configure workload nonce store: %w", err)
	}
	registry, err := cfg.WorkloadAuth.Registry()
	if err != nil {
		_ = db.Close()
		return err
	}
	verifier, err := workloadauth.NewVerifier(string(role), registry, workloadauth.SystemClock{}, nonces)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("configure workload verifier: %w", err)
	}
	workloadAuthDatabase = db
	workloadVerifier = verifier
	return nil
}

func signedWorkloadBoundary(role serviceRole, verifier workloadRequestVerifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if verifier == nil {
			return workloadAuthFailure(c, http.StatusServiceUnavailable, "workload_auth_unavailable")
		}
		req, err := fiberWorkloadRequest(c)
		if err != nil {
			return workloadAuthFailure(c, http.StatusUnauthorized, "workload_auth_failed")
		}
		principal, err := verifier.Verify(req, c.Body())
		if err != nil {
			if errors.Is(err, workloadauth.ErrNonceStore) {
				return workloadAuthFailure(c, http.StatusServiceUnavailable, "workload_auth_unavailable")
			}
			return workloadAuthFailure(c, http.StatusUnauthorized, "workload_auth_failed")
		}
		decodedPath, err := decodedFiberAuthorizationPath(c, req.URL)
		if err != nil {
			return workloadAuthFailure(c, http.StatusForbidden, "workload_capability_denied")
		}
		if !authorizeWorkload(role, principal.Caller, c.Method(), decodedPath) {
			if !workloadCapabilityExists(role, c.Method(), decodedPath) {
				return c.SendStatus(http.StatusNotFound)
			}
			return workloadAuthFailure(c, http.StatusForbidden, "workload_capability_denied")
		}
		c.Locals(workloadPrincipalLocal, principal)
		return c.Next()
	}
}

func workloadAuthFailure(c *fiber.Ctx, status int, code string) error {
	return c.Status(status).JSON(fiber.Map{"code": code, "message": "internal request authentication failed"})
}

func fiberWorkloadRequest(c *fiber.Ctx) (*http.Request, error) {
	if c == nil {
		return nil, workloadauth.ErrInvalidRequest
	}
	target := string(c.Context().RequestURI())
	parsed, err := url.ParseRequestURI(target)
	if err != nil {
		return nil, err
	}
	req := &http.Request{
		Method: c.Method(),
		URL:    parsed,
		Header: make(http.Header),
	}
	for _, name := range signedRequestHeaderNames() {
		for _, value := range c.Request().Header.PeekAll(name) {
			req.Header[name] = append(req.Header[name], string(value))
		}
	}
	return req.WithContext(c.UserContext()), nil
}

func signedRequestHeaderNames() []string {
	names := []string{"Content-Type", workloadauth.HeaderRequestID}
	names = append(names, workloadauth.IdentityHeaderNames()...)
	names = append(names, workloadauth.WorkloadHeaderNames()...)
	return names
}

func decodedFiberAuthorizationPath(c *fiber.Ctx, signedURL *url.URL) (string, error) {
	if c == nil || signedURL == nil {
		return "", workloadauth.ErrInvalidRequest
	}
	decoded, err := url.PathUnescape(signedURL.EscapedPath())
	if err != nil || !utf8.ValidString(decoded) || decoded != c.Path() {
		return "", workloadauth.ErrInvalidRequest
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", workloadauth.ErrInvalidRequest
		}
		if twiceDecoded, err := url.PathUnescape(segment); err == nil && twiceDecoded != segment {
			return "", workloadauth.ErrInvalidRequest
		}
	}
	return decoded, nil
}

func authorizeWorkload(role serviceRole, caller, method, path string) bool {
	for _, capability := range workloadCapabilities(role) {
		if capability.caller == caller && capability.method == method && matchWorkloadPath(capability.path, path) {
			return true
		}
	}
	return false
}

func workloadCapabilityExists(role serviceRole, method, path string) bool {
	for _, capability := range workloadCapabilities(role) {
		if capability.method == method && matchWorkloadPath(capability.path, path) {
			return true
		}
	}
	return false
}

func workloadCapabilities(role serviceRole) []workloadCapability {
	var capabilities []workloadCapability
	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.role == role {
			capabilities = append(capabilities, workloadCapability{
				caller: string(serviceRoleAPIGateway),
				method: spec.method,
				path:   spec.path,
			})
		}
	}
	add := func(caller, method, path string) {
		capabilities = append(capabilities, workloadCapability{caller: caller, method: method, path: path})
	}
	switch role {
	case serviceRoleIdentityAuth:
		add(string(serviceRoleAPIGateway), http.MethodPost, "/internal/identity-auth/principals/resolve")
		add(string(serviceRoleNotification), http.MethodPost, "/internal/identity-auth/users/resolve-batch")
		add(string(serviceRoleEBSAdapter), http.MethodPost, "/internal/identity-auth/users/by-mobile")
	case serviceRoleCardVault:
		add(string(serviceRoleIdentityAuth), http.MethodPost, "/internal/card-vault/cards/masked")
		for _, path := range []string{
			"/internal/card-vault/enrollment-intents",
			"/internal/card-vault/enrollment-intents/begin",
			"/internal/card-vault/enrollment-intents/claim-rail",
			"/internal/card-vault/enrollment-intents/complete",
			"/internal/card-vault/enrollment-intents/fail",
			"/internal/card-vault/funded-operations/claim",
		} {
			add(string(serviceRoleEBSAdapter), http.MethodPost, path)
		}
	case serviceRoleNotification:
		add(string(serviceRoleEBSAdapter), http.MethodPost, "/internal/notification-chat/push-data")
		add(string(serviceRoleEBSAdapter), http.MethodPost, "/internal/notification-chat/biller-hook")
	}
	return capabilities
}

func matchWorkloadPath(pattern, path string) bool {
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, expected := range patternParts {
		if expected == "*" {
			return index < len(pathParts)
		}
		if index >= len(pathParts) {
			return false
		}
		actual := pathParts[index]
		if strings.HasPrefix(expected, ":") {
			if actual == "" || actual == "." || actual == ".." {
				return false
			}
			continue
		}
		if expected != actual {
			return false
		}
	}
	return len(patternParts) == len(pathParts)
}
