package keycloakadmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantaccess"
	"github.com/coreos/go-oidc/v3/oidc"
)

const BootstrapOperatorEmail = "admin@noebs.sd"
const BootstrapOperatorUsername = "noebs-admin"
const bootstrapOperationAttribute = "noebs.bootstrap.operation_id"

var operatorSetupActions = []string{"VERIFY_EMAIL", "UPDATE_PASSWORD", "CONFIGURE_TOTP"}

type operatorIdentity struct {
	Attributes      map[string][]string `json:"attributes"`
	ID              string              `json:"id"`
	Username        string              `json:"username"`
	Email           string              `json:"email"`
	EmailVerified   bool                `json:"emailVerified"`
	Enabled         bool                `json:"enabled"`
	RequiredActions []string            `json:"requiredActions"`
}

func exactOperatorLookup(ctx context.Context, session *adminSession, field, value string) (*operatorIdentity, error) {
	query := url.Values{field: {value}, "exact": {"true"}, "first": {"0"}, "max": {"2"}, "briefRepresentation": {"false"}}
	var users []operatorIdentity
	found, err := session.get(ctx, realmPath("noebs")+"/users?"+query.Encode(), &users)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrUnexpectedResponse
	}
	if len(users) == 0 {
		return nil, nil
	}
	if len(users) != 1 || accessSubject(users[0].ID) != nil {
		return nil, tenantaccess.ErrOperationConflict
	}
	actual := users[0].Email
	if field == "username" {
		actual = users[0].Username
	}
	if actual != value {
		return nil, tenantaccess.ErrOperationConflict
	}
	return &users[0], nil
}

func (a *AccessAuthority) exactOperator(ctx context.Context, session *adminSession, expectedSubject string) (*operatorIdentity, error) {
	byEmail, err := exactOperatorLookup(ctx, session, "email", BootstrapOperatorEmail)
	if err != nil {
		return nil, err
	}
	byUsername, err := exactOperatorLookup(ctx, session, "username", BootstrapOperatorUsername)
	if err != nil {
		return nil, err
	}
	if byEmail == nil && byUsername == nil {
		if expectedSubject != "" {
			return nil, tenantaccess.ErrOperationConflict
		}
		return nil, nil
	}
	if byEmail == nil || byUsername == nil || byEmail.ID != byUsername.ID || byEmail.Username != BootstrapOperatorUsername || byUsername.Email != BootstrapOperatorEmail || !byEmail.Enabled || !byUsername.Enabled || expectedSubject != "" && byEmail.ID != expectedSubject {
		return nil, tenantaccess.ErrOperationConflict
	}
	account, err := a.enrollment.Inspect(ctx, byEmail.ID)
	if err != nil {
		return nil, err
	}
	for _, roles := range account.Memberships {
		if slices.Contains(roles, "user") {
			return nil, tenantaccess.ErrForbidden
		}
	}
	return byEmail, nil
}

// PrepareOperator is a bootstrap-only identity boundary, not a signup API.
// The deployment-selected email/handle are fixed and cannot merge an existing
// consumer identity. It never verifies an email or supplies a password.
func (a *AccessAuthority) PrepareOperator(ctx context.Context, tenant, email, expectedSubject, operationID string) (string, error) {
	if accessSubject(operationID) != nil || email != BootstrapOperatorEmail || expectedSubject != "" && accessSubject(expectedSubject) != nil {
		return "", tenantaccess.ErrInvalidRequest
	}
	if _, err := a.enrollment.catalog.Require(tenant); err != nil {
		return "", err
	}
	session, err := a.enrollment.client.session(ctx)
	if err != nil {
		return "", err
	}
	user, err := a.exactOperator(ctx, session, expectedSubject)
	if err != nil {
		return "", err
	}
	if user != nil && expectedSubject == "" && !slices.Equal(user.Attributes[bootstrapOperationAttribute], []string{operationID}) {
		return "", tenantaccess.ErrOperationConflict
	}
	if user == nil {
		err = session.post(ctx, realmPath("noebs")+"/users", map[string]any{"username": BootstrapOperatorUsername, "email": BootstrapOperatorEmail, "emailVerified": false, "enabled": true, "requiredActions": operatorSetupActions, "attributes": map[string][]string{bootstrapOperationAttribute: {operationID}}})
		if err != nil {
			var conflict *HTTPError
			if !errors.As(err, &conflict) || conflict.StatusCode != http.StatusConflict {
				return "", err
			}
		}
		user, err = a.exactOperator(ctx, session, "")
		if err != nil {
			return "", err
		}
		if user == nil || !slices.Equal(user.Attributes[bootstrapOperationAttribute], []string{operationID}) {
			return "", tenantaccess.ErrOperationConflict
		}
	}
	// Read the complete current action list, then update only requiredActions.
	// Re-sending enabled=true could clear a native temporary lockout; profile,
	// credentials and enabled/emailVerified fields are deliberately omitted.
	var representation map[string]any
	found, err := session.get(ctx, realmPath("noebs")+"/users/"+url.PathEscape(user.ID), &representation)
	if err != nil {
		return "", err
	}
	if !found || representation["id"] != user.ID || representation["username"] != BootstrapOperatorUsername || representation["email"] != BootstrapOperatorEmail || representation["enabled"] != true {
		return "", tenantaccess.ErrOperationConflict
	}
	emailVerified, ok := representation["emailVerified"].(bool)
	if !ok {
		return "", ErrUnexpectedResponse
	}
	actions := []string{}
	if raw, exists := representation["requiredActions"]; exists {
		values, ok := raw.([]any)
		if !ok {
			return "", ErrUnexpectedResponse
		}
		for _, value := range values {
			action, ok := value.(string)
			if !ok || action == "" {
				return "", ErrUnexpectedResponse
			}
			actions = append(actions, action)
		}
	}
	changed := false
	for _, action := range operatorSetupActions {
		if !slices.Contains(actions, action) {
			actions = append(actions, action)
			changed = true
		}
	}
	if changed {
		if err = session.put(ctx, realmPath("noebs")+"/users/"+url.PathEscape(user.ID), map[string]any{"requiredActions": actions}); err != nil {
			return "", err
		}
	}
	var verified operatorIdentity
	found, err = session.get(ctx, realmPath("noebs")+"/users/"+url.PathEscape(user.ID), &verified)
	if err != nil {
		return "", err
	}
	if !found || verified.ID != user.ID || verified.Username != BootstrapOperatorUsername || verified.Email != BootstrapOperatorEmail || verified.EmailVerified != emailVerified || !verified.Enabled {
		return "", tenantaccess.ErrOperationConflict
	}
	for _, action := range actions {
		if !slices.Contains(verified.RequiredActions, action) {
			return "", ErrMembershipVerification
		}
	}
	return user.ID, nil
}

// ServiceAccountSubject verifies the service token signature, configured issuer,
// realm-management audience and exact authorized client. User lookup does not
// reliably expose the native service-account association in Keycloak 26.7.
func (a *AccessAuthority) ServiceAccountSubject(ctx context.Context) (string, error) {
	session, err := a.enrollment.client.session(ctx)
	if err != nil {
		return "", err
	}
	config := a.enrollment.client.config
	verifyContext := oidc.ClientContext(ctx, a.enrollment.client.http)
	keys := oidc.NewRemoteKeySet(verifyContext, config.BaseURL+"/realms/noebs/protocol/openid-connect/certs")
	verifier := oidc.NewVerifier(a.issuer, keys, &oidc.Config{ClientID: "realm-management"})
	token, err := verifier.Verify(verifyContext, session.token)
	if err != nil {
		return "", err
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
		Username        string `json:"preferred_username"`
	}
	if err = token.Claims(&claims); err != nil {
		return "", err
	}
	if claims.AuthorizedParty != config.ClientID || accessSubject(token.Subject) != nil {
		return "", fmt.Errorf("%w: service token authorized-client=%t canonical-subject=%t", ErrUnexpectedResponse, claims.AuthorizedParty == config.ClientID, accessSubject(token.Subject) == nil)
	}
	return token.Subject, nil
}

// SendOperatorSetup sends Keycloak's native required-action link. The exact
// private terminal URI must be provisioned on noebs-backoffice by desired state.
// Delivery can be retried explicitly; this method never returns a secret link.
func (a *AccessAuthority) SendOperatorSetup(ctx context.Context, subject, backofficeOrigin string) error {
	if err := accessSubject(subject); err != nil {
		return err
	}
	issuer, err := url.Parse(a.issuer)
	if err != nil {
		return tenantaccess.ErrInvalidRequest
	}
	if err = backofficeauth.ValidateSeparateOrigin(backofficeOrigin, issuer.Scheme+"://"+issuer.Host); err != nil {
		return tenantaccess.ErrInvalidRequest
	}
	session, err := a.enrollment.client.session(ctx)
	if err != nil {
		return err
	}
	user, err := a.exactOperator(ctx, session, subject)
	if err != nil {
		return err
	}
	if user == nil {
		return tenantaccess.ErrNotFound
	}
	// Resending setup never reinstates already completed password/TOTP actions.
	var current operatorIdentity
	found, err := session.get(ctx, realmPath("noebs")+"/users/"+url.PathEscape(subject), &current)
	if err != nil {
		return err
	}
	if !found || current.ID != subject || current.Username != BootstrapOperatorUsername || current.Email != BootstrapOperatorEmail || !current.Enabled {
		return tenantaccess.ErrOperationConflict
	}
	actions := []string{}
	for _, action := range operatorSetupActions {
		if slices.Contains(current.RequiredActions, action) {
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		return nil
	}
	query := url.Values{"client_id": {"noebs-backoffice"}, "redirect_uri": {backofficeOrigin + "/backoffice/setup-complete"}, "lifespan": {"900"}}
	return session.put(ctx, realmPath("noebs")+"/users/"+url.PathEscape(subject)+"/execute-actions-email?"+query.Encode(), actions)
}

// ResolveOperator verifies the fixed identity without changing required actions.
// Completed bootstrap retries use this method instead of PrepareOperator.
func (a *AccessAuthority) ResolveOperator(ctx context.Context, tenant, email, expectedSubject string) (string, error) {
	if email != BootstrapOperatorEmail || expectedSubject != "" && accessSubject(expectedSubject) != nil {
		return "", tenantaccess.ErrInvalidRequest
	}
	if _, err := a.enrollment.catalog.Require(tenant); err != nil {
		return "", err
	}
	session, err := a.enrollment.client.session(ctx)
	if err != nil {
		return "", err
	}
	user, err := a.exactOperator(ctx, session, expectedSubject)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", tenantaccess.ErrNotFound
	}
	return user.ID, nil
}
