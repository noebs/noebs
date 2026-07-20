package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
)

const profileProjectionResolveTimeout = 3 * time.Second

var (
	errProfileProjectionNotFound    = errors.New("profile projection not found")
	errProfileProjectionUnavailable = errors.New("profile projection service unavailable")
)

type profileProjectionResolver interface {
	Resolve(context.Context, tenantauth.Principal, string, string) (int64, error)
}

type identityProfileProjectionResolver struct {
	endpoint string
	client   *http.Client
	signers  *workloadauth.SignerSet
}

func newIdentityProfileProjectionResolver(cfg ebs_fields.NoebsConfig, signers *workloadauth.SignerSet) (*identityProfileProjectionResolver, error) {
	if signers == nil {
		return nil, workloadauth.ErrMissingSigner
	}
	endpoint, err := serviceDiscoveryEndpoint(cfg, serviceRoleIdentityAuth)
	if err != nil {
		return nil, err
	}
	return &identityProfileProjectionResolver{
		endpoint: endpoint,
		client: newInternalHTTPClient(
			httpclient.WithTimeout(profileProjectionResolveTimeout),
			httpclient.WithResponseHeaderTimeout(2*time.Second),
		),
		signers: signers,
	}, nil
}

func (r *identityProfileProjectionResolver) Resolve(
	ctx context.Context,
	principal tenantauth.Principal,
	requestID string,
	sourceIP string,
) (int64, error) {
	if r == nil || r.client == nil || r.endpoint == "" || r.signers == nil || ctx == nil {
		return 0, errProfileProjectionUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/internal/identity-auth/principals/resolve", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set(workloadauth.HeaderRequestID, requestID)
	if err := setGatewayPrincipalHeaders(req.Header, principal, "", 0, sourceIP); err != nil {
		return 0, err
	}
	if err := r.signers.Sign(string(serviceRoleIdentityAuth), req, nil); err != nil {
		return 0, err
	}

	client := *r.client
	client.CheckRedirect = workloadauth.RejectRedirect
	response, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errProfileProjectionUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return 0, errProfileProjectionNotFound
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return 0, fmt.Errorf("%w: identity-auth returned %s", errProfileProjectionUnavailable, response.Status)
	}
	var result consumer.ResolveProfileProjectionResult
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return 0, fmt.Errorf("%w: %v", errProfileProjectionUnavailable, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return 0, errProfileProjectionUnavailable
	}
	if result.UserID <= 0 {
		return 0, errProfileProjectionUnavailable
	}
	return result.UserID, nil
}

func setGatewayPrincipalHeaders(
	header http.Header,
	principal tenantauth.Principal,
	permission tenantauth.Permission,
	userID int64,
	sourceIP string,
) error {
	identity := principal.Identity()
	parsedSource := net.ParseIP(sourceIP)
	if header == nil || principal.Tenant() == "" || principal.OrganizationID() == "" ||
		identity.Issuer == "" || identity.Subject == "" || identity.AuthorizedParty == "" ||
		identity.ExpiresAt.IsZero() || parsedSource == nil || userID < 0 {
		return errors.New("invalid gateway principal")
	}
	roles := principal.Roles()
	if len(roles) == 0 {
		return errors.New("invalid gateway principal roles")
	}
	slices.Sort(roles)
	encodedRoles := make([]string, len(roles))
	for index, role := range roles {
		encodedRoles[index] = string(role)
	}
	header.Set(workloadauth.HeaderTenantID, principal.Tenant())
	header.Set(workloadauth.HeaderIssuer, identity.Issuer)
	header.Set(workloadauth.HeaderSubject, identity.Subject)
	header.Set(workloadauth.HeaderOrganizationID, principal.OrganizationID())
	header.Set(workloadauth.HeaderAuthorizedParty, identity.AuthorizedParty)
	header.Set(workloadauth.HeaderRoles, strings.Join(encodedRoles, ","))
	header.Set(workloadauth.HeaderPermission, string(permission))
	if userID > 0 {
		header.Set(workloadauth.HeaderUserID, strconv.FormatInt(userID, 10))
	} else {
		header.Del(workloadauth.HeaderUserID)
	}
	header.Set(workloadauth.HeaderSourceIP, parsedSource.String())
	header.Set(workloadauth.HeaderTokenExpiresAt, strconv.FormatInt(identity.ExpiresAt.Unix(), 10))
	return nil
}
