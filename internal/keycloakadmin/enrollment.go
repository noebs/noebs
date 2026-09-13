package keycloakadmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantcatalog"
)

// EnrollmentAuthority exposes only account inspection and additive membership
// operations. Its service credential belongs to identity-auth, not the gateway.
type EnrollmentAuthority struct {
	client  *Reconciler
	catalog tenantcatalog.Catalog
}

func NewEnrollmentAuthority(policy accountenrollment.RuntimeConfig, catalog tenantcatalog.Catalog, client *http.Client) (*EnrollmentAuthority, error) {
	if err := policy.Validate(catalog, true); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, accountenrollment.ErrInvalidConfig
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &EnrollmentAuthority{catalog: catalog, client: &Reconciler{config: Config{BaseURL: policy.KeycloakBaseURL, AdminRealm: "noebs", ClientID: policy.KeycloakClientID, ClientSecret: policy.KeycloakClientSecret}, http: &copyClient}}, nil
}

func (a *EnrollmentAuthority) Inspect(ctx context.Context, subject string) (accountenrollment.Account, error) {
	if err := validateCanonicalSubject(subject); err != nil {
		return accountenrollment.Account{}, err
	}
	session, err := a.client.session(ctx)
	if err != nil {
		return accountenrollment.Account{}, err
	}
	var user struct {
		ID            string `json:"id"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"emailVerified"`
		Enabled       bool   `json:"enabled"`
		FirstName     string `json:"firstName"`
		LastName      string `json:"lastName"`
	}
	found, err := session.get(ctx, realmPath("noebs")+"/users/"+url.PathEscape(subject), &user)
	if err != nil {
		return accountenrollment.Account{}, err
	}
	if !found || user.ID != subject || !user.Enabled {
		return accountenrollment.Account{}, accountenrollment.ErrInvalidIdentity
	}
	result := accountenrollment.Account{Email: user.Email, EmailVerified: user.EmailVerified, Fullname: strings.TrimSpace(user.FirstName + " " + user.LastName), Memberships: map[string][]string{}}
	organizations, err := a.organizations(ctx, session)
	if err != nil {
		return result, err
	}
	for tenant, organization := range organizations {
		classes, member, err := enrollmentMemberClasses(ctx, session, organization.ID, subject)
		if err != nil {
			return result, err
		}
		if member {
			result.Memberships[tenant] = classes
		}
	}
	return result, nil
}

func (a *EnrollmentAuthority) organizations(ctx context.Context, session *adminSession) (map[string]organizationRepresentation, error) {
	organizations, err := listOrganizations(ctx, session, realmPath("noebs"))
	if err != nil {
		return nil, err
	}
	result := map[string]organizationRepresentation{}
	for _, organization := range organizations {
		if _, err := a.catalog.Require(organization.Alias); err != nil {
			continue
		}
		if organization.ID == "" {
			return nil, ErrMembershipTopology
		}
		if _, exists := result[organization.Alias]; exists {
			return nil, ErrMembershipTopology
		}
		result[organization.Alias] = organization
	}
	if len(result) != len(a.catalog.All()) {
		return nil, ErrMembershipTopology
	}
	return result, nil
}

func enrollmentMemberClasses(ctx context.Context, session *adminSession, organizationID, subject string) ([]string, bool, error) {
	base := realmPath("noebs") + "/organizations/" + url.PathEscape(organizationID) + "/members/" + url.PathEscape(subject)
	var member membershipUserRepresentation
	found, err := session.get(ctx, base, &member)
	if err != nil || !found {
		return nil, found, err
	}
	if member.ID != subject {
		return nil, false, ErrMembershipTopology
	}
	var groups []groupRepresentation
	found, err = session.get(ctx, base+"/groups?briefRepresentation=false&first=0&max=1000", &groups)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, ErrMembershipTopology
	}
	classes := make([]string, 0, len(groups))
	for _, group := range groups {
		classes = append(classes, group.Name)
	}
	return classes, true, nil
}

func (a *EnrollmentAuthority) EnsureUserMembership(ctx context.Context, subject, tenant string) error {
	if err := validateCanonicalSubject(subject); err != nil {
		return err
	}
	if _, err := a.catalog.Require(tenant); err != nil {
		return err
	}
	session, err := a.client.session(ctx)
	if err != nil {
		return err
	}
	organizations, err := a.organizations(ctx, session)
	if err != nil {
		return err
	}
	organization := organizations[tenant]
	classes, member, err := enrollmentMemberClasses(ctx, session, organization.ID, subject)
	if err != nil {
		return err
	}
	// A simultaneous operator assignment wins; never remove or downgrade it.
	if member && len(classes) > 0 {
		return nil
	}
	base := realmPath("noebs") + "/organizations/" + url.PathEscape(organization.ID)
	var groups []groupRepresentation
	if _, err := session.get(ctx, base+"/groups?briefRepresentation=false&populateHierarchy=false&first=0&max=1000", &groups); err != nil {
		return err
	}
	var userGroup string
	for _, group := range groups {
		if group.Name == "user" {
			if userGroup != "" || group.ID == "" {
				return ErrMembershipTopology
			}
			userGroup = group.ID
		}
	}
	if userGroup == "" {
		return ErrMembershipTopology
	}
	if !member {
		if err := session.post(ctx, base+"/members", subject); err != nil {
			var conflict *HTTPError
			if !errors.As(err, &conflict) || conflict.StatusCode != http.StatusConflict {
				return fmt.Errorf("add account membership: %w", err)
			}
		}
	}
	// Re-read after creating the membership: another actor may have assigned
	// an operator class while enrollment was in progress.
	classes, _, err = enrollmentMemberClasses(ctx, session, organization.ID, subject)
	if err != nil {
		return err
	}
	if len(classes) > 0 {
		return nil
	}
	return session.put(ctx, base+"/groups/"+url.PathEscape(userGroup)+"/members/"+url.PathEscape(subject), nil)
}
