package keycloakadmin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantaccess"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
)

// AccessAuthority uses the identity-auth enroller credential. It never changes
// role definitions/mappings or replaces a subject's complete membership set.
// The journaled tenantaccess service is its only production mutation caller.
type AccessAuthority struct {
	enrollment *EnrollmentAuthority
	issuer     string
}

var _ tenantaccess.Authority = (*AccessAuthority)(nil)

func NewAccessAuthority(policy accountenrollment.RuntimeConfig, catalog tenantcatalog.Catalog, client *http.Client) (*AccessAuthority, error) {
	if !policy.Enabled {
		return nil, accountenrollment.ErrInvalidConfig
	}
	enrollment, err := NewEnrollmentAuthority(policy, catalog, client)
	if err != nil {
		return nil, err
	}
	return &AccessAuthority{enrollment: enrollment, issuer: policy.Issuer}, nil
}

type accessOrganization struct {
	base   string
	groups map[tenantauth.Role]groupRepresentation
}

type accessMember struct {
	Enabled   *bool  `json:"enabled"`
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

func (a *AccessAuthority) selected(ctx context.Context, tenant string) (*adminSession, accessOrganization, error) {
	if a == nil || a.enrollment == nil {
		return nil, accessOrganization{}, ErrInvalidConfig
	}
	if _, err := a.enrollment.catalog.Require(tenant); err != nil {
		return nil, accessOrganization{}, err
	}
	session, err := a.enrollment.client.session(ctx)
	if err != nil {
		return nil, accessOrganization{}, err
	}
	organizations, err := a.enrollment.organizations(ctx, session)
	if err != nil {
		return nil, accessOrganization{}, err
	}
	organization := organizations[tenant]
	if !organization.Enabled || !slices.Equal(organization.Attributes[managedAttribute], []string{"true"}) {
		return nil, accessOrganization{}, ErrMembershipTopology
	}
	base := realmPath("noebs") + "/organizations/" + url.PathEscape(organization.ID)
	var groups []groupRepresentation
	found, err := session.get(ctx, base+"/groups?briefRepresentation=false&populateHierarchy=false&first=0&max=1000", &groups)
	if err != nil {
		return nil, accessOrganization{}, err
	}
	if !found || len(groups) >= 1000 {
		return nil, accessOrganization{}, ErrMembershipTopology
	}
	selected := accessOrganization{base: base, groups: map[tenantauth.Role]groupRepresentation{}}
	resourceID := ""
	for _, group := range groups {
		role, err := tenantauth.ParseTenantRole(group.Name)
		if err != nil {
			continue
		}
		if group.ID == "" || selected.groups[role].ID != "" || !slices.Equal(group.Attributes[managedAttribute], []string{"true"}) {
			return nil, accessOrganization{}, ErrMembershipTopology
		}
		children, err := listOrganizationGroupChildren(ctx, session, base+"/groups", group)
		if err != nil {
			return nil, accessOrganization{}, err
		}
		if len(children) != 0 {
			return nil, accessOrganization{}, ErrMembershipTopology
		}
		var mappings roleMappingsRepresentation
		found, err := session.get(ctx, base+"/groups/"+url.PathEscape(group.ID)+"/role-mappings", &mappings)
		if err != nil {
			return nil, accessOrganization{}, err
		}
		if !found || len(mappings.RealmMappings) != 0 || len(mappings.ClientMappings) != 1 {
			return nil, accessOrganization{}, ErrMembershipTopology
		}
		mapping, ok := mappings.ClientMappings["noebs-api"]
		if !ok || mapping.Client != "noebs-api" || mapping.ID == "" || resourceID != "" && resourceID != mapping.ID {
			return nil, accessOrganization{}, ErrMembershipTopology
		}
		resourceID = mapping.ID
		wanted := []string{string(role)}
		for _, permission := range tenantauth.PermissionsForRoles([]tenantauth.Role{role}) {
			wanted = append(wanted, string(permission))
		}
		slices.Sort(wanted)
		names := make([]string, 0, len(mapping.Mappings))
		for _, mapped := range mapping.Mappings {
			if mapped.ID == "" || mapped.Composite || !mapped.ClientRole || mapped.ContainerID != mapping.ID {
				return nil, accessOrganization{}, ErrMembershipTopology
			}
			names = append(names, mapped.Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, wanted) {
			return nil, accessOrganization{}, ErrMembershipTopology
		}
		selected.groups[role] = group
	}
	if len(selected.groups) != 3 {
		return nil, accessOrganization{}, ErrMembershipTopology
	}
	return session, selected, nil
}

func accessSubject(subject string) error {
	if err := validateCanonicalSubject(subject); err != nil {
		return tenantaccess.ErrInvalidRequest
	}
	return nil
}

func (a *AccessAuthority) SubjectExists(ctx context.Context, subject string) (bool, error) {
	if err := accessSubject(subject); err != nil {
		return false, err
	}
	session, err := a.enrollment.client.session(ctx)
	if err != nil {
		return false, err
	}
	var user accessMember
	found, err := session.get(ctx, realmPath("noebs")+"/users/"+url.PathEscape(subject), &user)
	if err != nil || !found {
		return false, err
	}
	if user.ID != subject {
		return false, ErrMembershipTopology
	}
	return true, nil
}

func (a *AccessAuthority) Account(ctx context.Context, tenant, subject string) (tenantaccess.AuthorityAccount, error) {
	if err := accessSubject(subject); err != nil {
		return tenantaccess.AuthorityAccount{}, err
	}
	session, organization, err := a.selected(ctx, tenant)
	if err != nil {
		return tenantaccess.AuthorityAccount{}, err
	}
	return readAccessAccount(ctx, session, organization, subject)
}

func readAccessAccount(ctx context.Context, session *adminSession, organization accessOrganization, subject string) (tenantaccess.AuthorityAccount, error) {
	result := tenantaccess.AuthorityAccount{Subject: subject, Roles: []tenantauth.Role{}}
	base := organization.base + "/members/" + url.PathEscape(subject)
	var user accessMember
	found, err := session.get(ctx, base, &user)
	if err != nil || !found {
		return result, err
	}
	if user.ID != subject || user.Enabled == nil {
		return result, ErrMembershipTopology
	}
	result.Member = true
	result.Disabled = !*user.Enabled
	result.Email = user.Email
	result.Fullname = strings.TrimSpace(user.FirstName + " " + user.LastName)
	var groups []groupRepresentation
	found, err = session.get(ctx, base+"/groups?briefRepresentation=false&first=0&max=1000", &groups)
	if err != nil {
		return result, err
	}
	if !found || len(groups) >= 1000 {
		return result, ErrMembershipTopology
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if group.ID == "" || seen[group.ID] {
			return result, ErrMembershipTopology
		}
		seen[group.ID] = true
		role, err := tenantauth.ParseTenantRole(group.Name)
		if err == nil {
			if organization.groups[role].ID != group.ID {
				return result, ErrMembershipTopology
			}
			result.Roles = append(result.Roles, role)
			continue
		}
		// Unrelated groups remain untouched, but cannot be a hidden source of Noebs
		// API privileges that would make the reported managed role set inaccurate.
		var mappings roleMappingsRepresentation
		found, err = session.get(ctx, organization.base+"/groups/"+url.PathEscape(group.ID)+"/role-mappings", &mappings)
		if err != nil {
			return result, err
		}
		if !found {
			return result, ErrMembershipTopology
		}
		if mapping, ok := mappings.ClientMappings["noebs-api"]; ok && len(mapping.Mappings) > 0 {
			return result, ErrMembershipTopology
		}
		if len(mappings.RealmMappings) > 0 {
			return result, ErrMembershipTopology
		}
		for _, mapping := range mappings.ClientMappings {
			for _, role := range mapping.Mappings {
				if role.Composite || !role.ClientRole || role.ContainerID != mapping.ID {
					return result, ErrMembershipTopology
				}
			}
		}
	}
	slices.Sort(result.Roles)
	return result, nil
}

func (a *AccessAuthority) Members(ctx context.Context, tenant string) ([]tenantaccess.AuthorityAccount, error) {
	session, organization, err := a.selected(ctx, tenant)
	if err != nil {
		return nil, err
	}
	result := []tenantaccess.AuthorityAccount{}
	seen := map[string]bool{}
	for first := 0; first < 10000; first += 100 {
		var members []accessMember
		found, err := session.get(ctx, organization.base+"/members?first="+strconv.Itoa(first)+"&max=100", &members)
		if err != nil {
			return nil, err
		}
		if !found || len(members) > 100 {
			return nil, ErrMembershipTopology
		}
		for _, member := range members {
			if accessSubject(member.ID) != nil || seen[member.ID] {
				return nil, ErrMembershipTopology
			}
			seen[member.ID] = true
			account, err := readAccessAccount(ctx, session, organization, member.ID)
			if err != nil {
				return nil, err
			}
			if !account.Member {
				return nil, ErrMembershipTopology
			}
			result = append(result, account)
		}
		if len(members) < 100 {
			slices.SortFunc(result, func(a, b tenantaccess.AuthorityAccount) int { return strings.Compare(a.Subject, b.Subject) })
			return result, nil
		}
	}
	// Fail closed rather than silently presenting a truncated tenant list.
	return nil, ErrMembershipTopology
}

func (a *AccessAuthority) ApplyDelta(ctx context.Context, tenant, subject string, grant, revoke []tenantauth.Role) error {
	if err := accessSubject(subject); err != nil {
		return err
	}
	if len(grant)+len(revoke) == 0 {
		return tenantaccess.ErrInvalidRequest
	}
	for _, roles := range [][]tenantauth.Role{grant, revoke} {
		if !slices.IsSorted(roles) {
			return tenantaccess.ErrInvalidRequest
		}
		for i, role := range roles {
			if _, err := tenantauth.ParseTenantRole(string(role)); err != nil || i > 0 && role == roles[i-1] {
				return tenantaccess.ErrInvalidRequest
			}
		}
	}
	for _, role := range grant {
		if slices.Contains(revoke, role) {
			return tenantaccess.ErrInvalidRequest
		}
	}
	session, organization, err := a.selected(ctx, tenant)
	if err != nil {
		return err
	}
	before, err := readAccessAccount(ctx, session, organization, subject)
	if err != nil {
		return err
	}
	if !before.Member && len(grant) == 0 {
		return nil
	}
	if !before.Member {
		if err = session.post(ctx, organization.base+"/members", subject); err != nil {
			var conflict *HTTPError
			if !errors.As(err, &conflict) || conflict.StatusCode != http.StatusConflict {
				return err
			}
		}
		before, err = readAccessAccount(ctx, session, organization, subject)
		if err != nil || !before.Member {
			if err != nil {
				return err
			}
			return ErrMembershipTopology
		}
	}
	// Remove requested grants first so partial progress never keeps a revoked
	// role simply because adding another role failed. The journal owns recovery.
	for _, role := range revoke {
		if !slices.Contains(before.Roles, role) {
			continue
		}
		if err = session.delete(ctx, organization.base+"/groups/"+url.PathEscape(organization.groups[role].ID)+"/members/"+url.PathEscape(subject), nil); err != nil {
			return err
		}
	}
	for _, role := range grant {
		if slices.Contains(before.Roles, role) {
			continue
		}
		if err = session.put(ctx, organization.base+"/groups/"+url.PathEscape(organization.groups[role].ID)+"/members/"+url.PathEscape(subject), nil); err != nil {
			return err
		}
	}
	after, err := readAccessAccount(ctx, session, organization, subject)
	if err != nil {
		return err
	}
	for _, role := range grant {
		if !slices.Contains(after.Roles, role) {
			return ErrMembershipVerification
		}
	}
	for _, role := range revoke {
		if slices.Contains(after.Roles, role) {
			return ErrMembershipVerification
		}
	}
	for _, role := range before.Roles {
		if !slices.Contains(revoke, role) && !slices.Contains(after.Roles, role) {
			return ErrMembershipVerification
		}
	}
	return nil
}
