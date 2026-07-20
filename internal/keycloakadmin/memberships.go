package keycloakadmin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"sort"
	"strings"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/google/uuid"
)

const MembershipsAPIVersion = "noebs.sd/keycloak-memberships/v1"

var (
	ErrInvalidMemberships       = errors.New("invalid Keycloak memberships")
	ErrInvalidLookupEmail       = errors.New("invalid Keycloak user lookup email")
	ErrMembershipSubjectMissing = errors.New("keycloak membership subject does not exist")
	ErrMembershipSubjectMany    = errors.New("keycloak user lookup is ambiguous")
	ErrMembershipRead           = errors.New("read Keycloak memberships")
	ErrMembershipTopology       = errors.New("keycloak membership topology does not match desired state")
	ErrMembershipApply          = errors.New("apply Keycloak memberships")
	ErrMembershipVerification   = errors.New("verify Keycloak memberships")
)

type MembershipClass string

const (
	MembershipClassUser        MembershipClass = "user"
	MembershipClassBackoffice  MembershipClass = "backoffice"
	MembershipClassTenantAdmin MembershipClass = "tenant-admin"
)

var membershipClassOrder = []MembershipClass{
	MembershipClassUser,
	MembershipClassBackoffice,
	MembershipClassTenantAdmin,
}

type TenantMembership struct {
	Tenant string          `yaml:"tenant"`
	Class  MembershipClass `yaml:"class"`
}

type Memberships struct {
	APIVersion  string             `yaml:"api_version"`
	Subject     string             `yaml:"subject"`
	Memberships []TenantMembership `yaml:"memberships"`
}

type MembershipAction string

const (
	MembershipActionAdd      MembershipAction = "add"
	MembershipActionSetClass MembershipAction = "set-class"
	MembershipActionRemove   MembershipAction = "remove"
)

type PlannedMembershipAction struct {
	Subject string
	Tenant  string
	Class   MembershipClass
	Action  MembershipAction
}

func LoadMemberships(reader io.Reader, catalog tenantcatalog.Catalog, state DesiredState) (Memberships, error) {
	var memberships Memberships
	if err := decodeYAML(reader, &memberships); err != nil {
		return Memberships{}, fmt.Errorf("%w: %v", ErrInvalidMemberships, err)
	}
	if memberships.APIVersion != MembershipsAPIVersion {
		return Memberships{}, fmt.Errorf("%w: api_version must be %q", ErrInvalidMemberships, MembershipsAPIVersion)
	}
	parsedSubject, err := uuid.Parse(memberships.Subject)
	if err != nil || parsedSubject == uuid.Nil || parsedSubject.String() != memberships.Subject {
		return Memberships{}, fmt.Errorf("%w: subject must be a canonical non-zero UUID", ErrInvalidMemberships)
	}
	if memberships.Memberships == nil {
		return Memberships{}, fmt.Errorf("%w: memberships must be an explicit sequence", ErrInvalidMemberships)
	}
	if err := validateMembershipAuthority(catalog, state); err != nil {
		return Memberships{}, err
	}

	organizationAliases := make(map[string]struct{}, len(state.Organizations))
	for _, organization := range state.Organizations {
		organizationAliases[organization.Alias] = struct{}{}
	}
	seen := make(map[string]struct{}, len(memberships.Memberships))
	order := make(map[string]int, len(catalog.All()))
	for index, tenant := range catalog.All() {
		order[string(tenant.ID)] = index
	}
	for index, membership := range memberships.Memberships {
		if _, err := catalog.Require(membership.Tenant); err != nil {
			return Memberships{}, fmt.Errorf("%w: memberships[%d].tenant: %v", ErrInvalidMemberships, index, err)
		}
		if _, exists := organizationAliases[membership.Tenant]; !exists {
			return Memberships{}, fmt.Errorf("%w: memberships[%d].tenant is not a desired organization", ErrInvalidMemberships, index)
		}
		if !isMembershipClass(membership.Class) {
			return Memberships{}, fmt.Errorf("%w: memberships[%d].class must be user, backoffice, or tenant-admin", ErrInvalidMemberships, index)
		}
		if _, exists := seen[membership.Tenant]; exists {
			return Memberships{}, fmt.Errorf("%w: duplicate tenant %q", ErrInvalidMemberships, membership.Tenant)
		}
		seen[membership.Tenant] = struct{}{}
	}
	sort.Slice(memberships.Memberships, func(i, j int) bool {
		return order[memberships.Memberships[i].Tenant] < order[memberships.Memberships[j].Tenant]
	})
	return memberships, nil
}

func validateMembershipAuthority(catalog tenantcatalog.Catalog, state DesiredState) error {
	if err := state.Validate(); err != nil {
		return fmt.Errorf("%w: desired state: %v", ErrInvalidMemberships, err)
	}
	tenants := catalog.All()
	if len(tenants) == 0 || len(tenants) != len(state.Organizations) {
		return fmt.Errorf("%w: tenant catalog and desired organizations must exactly match", ErrInvalidMemberships)
	}
	organizations := make(map[string]Organization, len(state.Organizations))
	for _, organization := range state.Organizations {
		organizations[organization.Alias] = organization
	}
	for _, tenant := range tenants {
		organization, exists := organizations[string(tenant.ID)]
		if !exists || organization.Name != tenant.Name {
			return fmt.Errorf("%w: tenant catalog and desired organizations must exactly match", ErrInvalidMemberships)
		}
	}
	return nil
}

func isMembershipClass(class MembershipClass) bool {
	for _, candidate := range membershipClassOrder {
		if class == candidate {
			return true
		}
	}
	return false
}

func (r *Reconciler) LookupSubjectByEmail(ctx context.Context, email string) (string, error) {
	if err := r.requireRealmLocal("noebs"); err != nil {
		return "", err
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.TrimSpace(email) != email {
		return "", ErrInvalidLookupEmail
	}
	session, err := r.session(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: create admin session: %w", ErrMembershipRead, err)
	}
	query := url.Values{
		"email": {email},
		"exact": {"true"},
		"first": {"0"},
		"max":   {"2"},
	}
	var users []membershipUserRepresentation
	if _, err := session.get(ctx, realmPath(r.config.AdminRealm)+"/users?"+query.Encode(), &users); err != nil {
		return "", fmt.Errorf("%w: exact user lookup: %w", ErrMembershipRead, err)
	}
	switch len(users) {
	case 0:
		return "", ErrMembershipSubjectMissing
	case 1:
	case 2:
		return "", ErrMembershipSubjectMany
	default:
		return "", fmt.Errorf("%w: exact user lookup returned more than two users", ErrMembershipSubjectMany)
	}
	if !strings.EqualFold(users[0].Email, email) {
		return "", fmt.Errorf("%w: Keycloak returned a non-matching email", ErrMembershipRead)
	}
	if err := validateCanonicalSubject(users[0].ID); err != nil {
		return "", fmt.Errorf("%w: exact user lookup returned an invalid subject", ErrMembershipRead)
	}
	return users[0].ID, nil
}

func (r *Reconciler) AssignMemberships(ctx context.Context, state DesiredState, desired Memberships, dryRun bool) ([]PlannedMembershipAction, error) {
	if err := validateMembershipAuthority(state.tenantCatalog, state); err != nil {
		return nil, err
	}
	if err := r.requireRealmLocal(state.Realm.Name); err != nil {
		return nil, err
	}
	if desired.APIVersion != MembershipsAPIVersion {
		return nil, fmt.Errorf("%w: memberships were not loaded and validated", ErrInvalidMemberships)
	}
	if err := validateCanonicalSubject(desired.Subject); err != nil {
		return nil, fmt.Errorf("%w: memberships were not loaded and validated", ErrInvalidMemberships)
	}
	session, err := r.session(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: create admin session: %w", ErrMembershipRead, err)
	}
	topology, err := readMembershipTopology(ctx, session, state)
	if err != nil {
		return nil, err
	}
	current, err := readSubjectMemberships(ctx, session, state, topology, desired.Subject)
	if err != nil {
		return nil, err
	}
	actions, steps := planMembershipChanges(state, topology, current, desired)
	if dryRun {
		return actions, nil
	}
	for _, step := range steps {
		if err := applyMembershipStep(ctx, session, state.Realm.Name, desired.Subject, step); err != nil {
			return actions, fmt.Errorf("%w: subject %s tenant %s action %s: %w", ErrMembershipApply, desired.Subject, step.action.Tenant, step.action.Action, err)
		}
	}
	verifiedTopology, err := readMembershipTopology(ctx, session, state)
	if err != nil {
		return actions, fmt.Errorf("%w: %w", ErrMembershipVerification, err)
	}
	verified, err := readSubjectMemberships(ctx, session, state, verifiedTopology, desired.Subject)
	if err != nil {
		return actions, fmt.Errorf("%w: %w", ErrMembershipVerification, err)
	}
	if err := verifySubjectMemberships(state, verified, desired); err != nil {
		return actions, err
	}
	return actions, nil
}

func (r *Reconciler) requireRealmLocal(realm string) error {
	if r.config.AdminRealm != realm || r.config.ClientID != "noebs-keycloak-reconciler" {
		return fmt.Errorf("%w: membership operations require the realm-local reconciler client", ErrInvalidConfig)
	}
	return nil
}

type membershipUserRepresentation struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type membershipOrganization struct {
	representation organizationRepresentation
	groups         map[MembershipClass]groupRepresentation
}

type membershipTopology map[string]membershipOrganization

type subjectOrganizationMembership struct {
	member  bool
	classes map[MembershipClass]bool
}

type subjectMemberships map[string]subjectOrganizationMembership

func readMembershipTopology(ctx context.Context, session *adminSession, state DesiredState) (membershipTopology, error) {
	base := realmPath(state.Realm.Name)
	resourceClient, clientRoles, err := readMembershipRoleAuthority(ctx, session, base, state)
	if err != nil {
		return nil, err
	}
	organizations, err := listOrganizations(ctx, session, base)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMembershipRead, err)
	}
	if len(organizations) != len(state.Organizations) {
		return nil, fmt.Errorf("%w: organizations differ", ErrMembershipTopology)
	}
	runtimeOrganizations := make(map[string]organizationRepresentation, len(organizations))
	for _, organization := range organizations {
		if organization.ID == "" {
			return nil, fmt.Errorf("%w: organization %q has no id", ErrMembershipTopology, organization.Alias)
		}
		if _, exists := runtimeOrganizations[organization.Alias]; exists {
			return nil, fmt.Errorf("%w: duplicate organization alias %q", ErrMembershipTopology, organization.Alias)
		}
		runtimeOrganizations[organization.Alias] = organization
	}

	topology := make(membershipTopology, len(state.Organizations))
	for _, desiredOrganization := range state.Organizations {
		organization, exists := runtimeOrganizations[desiredOrganization.Alias]
		wantedOrganization := organizationRepresentation{
			Name:       desiredOrganization.Name,
			Alias:      desiredOrganization.Alias,
			Enabled:    true,
			Attributes: managedAttributes(desiredOrganization.Attributes),
		}
		if !exists || !organizationMatches(organization, wantedOrganization) {
			return nil, fmt.Errorf("%w: organization %q differs", ErrMembershipTopology, desiredOrganization.Alias)
		}
		organizationGroupBase := base + "/organizations/" + url.PathEscape(organization.ID) + "/groups"
		groupPath := organizationGroupBase + "?briefRepresentation=false&populateHierarchy=false&first=0&max=1000"
		var groups []groupRepresentation
		if _, err := session.get(ctx, groupPath, &groups); err != nil {
			return nil, fmt.Errorf("%w: list groups for organization %s: %w", ErrMembershipRead, desiredOrganization.Alias, err)
		}
		if len(groups) != len(desiredOrganization.Groups) {
			return nil, fmt.Errorf("%w: groups for organization %q differ", ErrMembershipTopology, desiredOrganization.Alias)
		}
		groupsByName := make(map[string]groupRepresentation, len(groups))
		for _, group := range groups {
			if group.ID == "" {
				return nil, fmt.Errorf("%w: group %q in organization %q has no id", ErrMembershipTopology, group.Name, desiredOrganization.Alias)
			}
			if _, exists := groupsByName[group.Name]; exists {
				return nil, fmt.Errorf("%w: duplicate group %q in organization %q", ErrMembershipTopology, group.Name, desiredOrganization.Alias)
			}
			groupsByName[group.Name] = group
		}
		managedGroups := make(map[MembershipClass]groupRepresentation, len(desiredOrganization.Groups))
		for _, desiredGroup := range desiredOrganization.Groups {
			group, exists := groupsByName[desiredGroup.Name]
			wantedGroup := groupRepresentation{
				Name:        desiredGroup.Name,
				Description: desiredGroup.Description,
				Attributes:  managedAttributes(desiredGroup.Attributes),
			}
			class := MembershipClass(desiredGroup.Name)
			if !exists || !isMembershipClass(class) || !groupMatches(group, wantedGroup) {
				return nil, fmt.Errorf("%w: group %q in organization %q differs", ErrMembershipTopology, desiredGroup.Name, desiredOrganization.Alias)
			}
			children, err := listOrganizationGroupChildren(ctx, session, organizationGroupBase, group)
			if err != nil {
				return nil, fmt.Errorf("%w: list children for group %q in organization %q: %w", ErrMembershipRead, desiredGroup.Name, desiredOrganization.Alias, err)
			}
			if len(children) != 0 {
				return nil, fmt.Errorf("%w: group %q in organization %q has undeclared descendants", ErrMembershipTopology, desiredGroup.Name, desiredOrganization.Alias)
			}
			if err := verifyMembershipGroupRoleMappings(ctx, session, organizationGroupBase, group, desiredGroup, resourceClient, clientRoles); err != nil {
				if errors.Is(err, ErrMembershipRead) {
					return nil, fmt.Errorf("%w: group %q in organization %q", err, desiredGroup.Name, desiredOrganization.Alias)
				}
				return nil, fmt.Errorf("%w: group %q in organization %q: %v", ErrMembershipTopology, desiredGroup.Name, desiredOrganization.Alias, err)
			}
			managedGroups[class] = group
		}
		topology[desiredOrganization.Alias] = membershipOrganization{representation: organization, groups: managedGroups}
	}
	return topology, nil
}

func readMembershipRoleAuthority(ctx context.Context, session *adminSession, realmBase string, state DesiredState) (clientRepresentation, map[string]roleRepresentation, error) {
	client, found, err := findClient(ctx, session, realmBase, state.ResourceClient.ClientID)
	if err != nil {
		return clientRepresentation{}, nil, fmt.Errorf("%w: %w", ErrMembershipRead, err)
	}
	if !found || client.ID == "" {
		return clientRepresentation{}, nil, fmt.Errorf("%w: resource client %q differs", ErrMembershipTopology, state.ResourceClient.ClientID)
	}

	path := realmBase + "/clients/" + url.PathEscape(client.ID) + "/roles?briefRepresentation=false"
	var current []roleRepresentation
	found, err = session.get(ctx, path, &current)
	if err != nil {
		return clientRepresentation{}, nil, fmt.Errorf("%w: list resource-client roles: %w", ErrMembershipRead, err)
	}
	if !found || len(current) != len(state.ResourceClient.Roles) {
		return clientRepresentation{}, nil, fmt.Errorf("%w: resource-client roles differ", ErrMembershipTopology)
	}

	roles := make(map[string]roleRepresentation, len(current))
	for _, role := range current {
		if role.ID == "" || role.Name == "" {
			return clientRepresentation{}, nil, fmt.Errorf("%w: resource-client role identity differs", ErrMembershipTopology)
		}
		if _, exists := roles[role.Name]; exists {
			return clientRepresentation{}, nil, fmt.Errorf("%w: duplicate resource-client role %q", ErrMembershipTopology, role.Name)
		}
		roles[role.Name] = role
	}
	for _, desired := range state.ResourceClient.Roles {
		role, exists := roles[desired.Name]
		if !exists || role.Description != managedDescription(desired.Description) || role.Composite || !role.ClientRole || role.ContainerID != client.ID {
			return clientRepresentation{}, nil, fmt.Errorf("%w: resource-client role %q differs", ErrMembershipTopology, desired.Name)
		}
	}
	return client, roles, nil
}

func verifyMembershipGroupRoleMappings(
	ctx context.Context,
	session *adminSession,
	organizationGroupBase string,
	group groupRepresentation,
	desired OrganizationGroup,
	client clientRepresentation,
	clientRoles map[string]roleRepresentation,
) error {
	path := organizationGroupBase + "/" + url.PathEscape(group.ID) + "/role-mappings"
	var mappings roleMappingsRepresentation
	found, err := session.get(ctx, path, &mappings)
	if err != nil {
		return fmt.Errorf("%w: read role mappings: %w", ErrMembershipRead, err)
	}
	if !found || len(mappings.RealmMappings) != 0 || len(mappings.ClientMappings) != 1 {
		return errors.New("role mappings differ")
	}
	mapping, exists := mappings.ClientMappings[client.ClientID]
	if !exists || mapping.ID != client.ID || mapping.Client != client.ClientID || len(mapping.Mappings) != len(desired.ClientRoles) {
		return errors.New("role mappings differ")
	}

	seen := make(map[string]struct{}, len(mapping.Mappings))
	for _, mapped := range mapping.Mappings {
		role, exists := clientRoles[mapped.Name]
		if !exists || mapped.ID != role.ID || mapped.Composite || !mapped.ClientRole || mapped.ContainerID != client.ID {
			return errors.New("role mappings differ")
		}
		if _, duplicate := seen[mapped.Name]; duplicate {
			return errors.New("role mappings differ")
		}
		seen[mapped.Name] = struct{}{}
	}
	for _, role := range desired.ClientRoles {
		if _, exists := seen[role]; !exists {
			return errors.New("role mappings differ")
		}
	}
	return nil
}

func readSubjectMemberships(ctx context.Context, session *adminSession, state DesiredState, topology membershipTopology, subject string) (subjectMemberships, error) {
	base := realmPath(state.Realm.Name)
	var user membershipUserRepresentation
	found, err := session.get(ctx, base+"/users/"+url.PathEscape(subject), &user)
	if err != nil {
		return nil, fmt.Errorf("%w: get subject: %w", ErrMembershipRead, err)
	}
	if !found {
		return nil, ErrMembershipSubjectMissing
	}
	if user.ID != subject {
		return nil, fmt.Errorf("%w: Keycloak returned a different subject", ErrMembershipRead)
	}

	result := make(subjectMemberships, len(state.Organizations))
	for _, desiredOrganization := range state.Organizations {
		organization := topology[desiredOrganization.Alias]
		memberBase := base + "/organizations/" + url.PathEscape(organization.representation.ID) + "/members/" + url.PathEscape(subject)
		var member membershipUserRepresentation
		found, err := session.get(ctx, memberBase, &member)
		if err != nil {
			return nil, fmt.Errorf("%w: get membership for tenant %s: %w", ErrMembershipRead, desiredOrganization.Alias, err)
		}
		current := subjectOrganizationMembership{member: found, classes: make(map[MembershipClass]bool)}
		if !found {
			result[desiredOrganization.Alias] = current
			continue
		}
		if member.ID != subject {
			return nil, fmt.Errorf("%w: tenant %s returned a different subject", ErrMembershipRead, desiredOrganization.Alias)
		}
		var groups []groupRepresentation
		found, err = session.get(ctx, memberBase+"/groups?briefRepresentation=false&first=0&max=1000", &groups)
		if err != nil {
			return nil, fmt.Errorf("%w: list subject groups for tenant %s: %w", ErrMembershipRead, desiredOrganization.Alias, err)
		}
		if !found {
			return nil, fmt.Errorf("%w: subject groups endpoint is missing for tenant %s", ErrMembershipTopology, desiredOrganization.Alias)
		}
		for _, group := range groups {
			class, exists := membershipClassForGroup(organization.groups, group)
			if !exists {
				return nil, fmt.Errorf("%w: subject belongs to unknown group %q in tenant %s", ErrMembershipTopology, group.Name, desiredOrganization.Alias)
			}
			if current.classes[class] {
				return nil, fmt.Errorf("%w: duplicate subject group %q in tenant %s", ErrMembershipTopology, group.Name, desiredOrganization.Alias)
			}
			current.classes[class] = true
		}
		result[desiredOrganization.Alias] = current
	}
	return result, nil
}

func membershipClassForGroup(groups map[MembershipClass]groupRepresentation, group groupRepresentation) (MembershipClass, bool) {
	for class, managedGroup := range groups {
		if group.ID == managedGroup.ID && group.Name == managedGroup.Name {
			return class, true
		}
	}
	return "", false
}

type membershipStep struct {
	action       PlannedMembershipAction
	organization membershipOrganization
	current      subjectOrganizationMembership
}

func planMembershipChanges(state DesiredState, topology membershipTopology, current subjectMemberships, desired Memberships) ([]PlannedMembershipAction, []membershipStep) {
	desiredByTenant := make(map[string]MembershipClass, len(desired.Memberships))
	for _, membership := range desired.Memberships {
		desiredByTenant[membership.Tenant] = membership.Class
	}
	var actions []PlannedMembershipAction
	var steps []membershipStep
	for _, tenant := range state.tenantCatalog.All() {
		tenantID := string(tenant.ID)
		wantedClass, wanted := desiredByTenant[tenantID]
		existing := current[tenantID]
		action := PlannedMembershipAction{Subject: desired.Subject, Tenant: tenantID, Class: wantedClass}
		switch {
		case !wanted && existing.member:
			action.Action = MembershipActionRemove
		case wanted && !existing.member:
			action.Action = MembershipActionAdd
		case wanted && !exactMembershipClass(existing.classes, wantedClass):
			action.Action = MembershipActionSetClass
		default:
			continue
		}
		actions = append(actions, action)
		steps = append(steps, membershipStep{action: action, organization: topology[tenantID], current: existing})
	}
	return actions, steps
}

func exactMembershipClass(classes map[MembershipClass]bool, wanted MembershipClass) bool {
	return len(classes) == 1 && classes[wanted]
}

func applyMembershipStep(ctx context.Context, session *adminSession, realm, subject string, step membershipStep) error {
	organizationBase := realmPath(realm) + "/organizations/" + url.PathEscape(step.organization.representation.ID)
	switch step.action.Action {
	case MembershipActionAdd:
		if err := session.post(ctx, organizationBase+"/members", subject); err != nil {
			return fmt.Errorf("add organization member: %w", err)
		}
		group := step.organization.groups[step.action.Class]
		if err := session.put(ctx, organizationBase+"/groups/"+url.PathEscape(group.ID)+"/members/"+url.PathEscape(subject), nil); err != nil {
			return fmt.Errorf("add group member: %w", err)
		}
	case MembershipActionSetClass:
		for _, class := range membershipClassOrder {
			if class == step.action.Class || !step.current.classes[class] {
				continue
			}
			group := step.organization.groups[class]
			if err := session.delete(ctx, organizationBase+"/groups/"+url.PathEscape(group.ID)+"/members/"+url.PathEscape(subject), nil); err != nil {
				return fmt.Errorf("remove group member: %w", err)
			}
		}
		if !step.current.classes[step.action.Class] {
			group := step.organization.groups[step.action.Class]
			if err := session.put(ctx, organizationBase+"/groups/"+url.PathEscape(group.ID)+"/members/"+url.PathEscape(subject), nil); err != nil {
				return fmt.Errorf("add group member: %w", err)
			}
		}
	case MembershipActionRemove:
		if err := session.delete(ctx, organizationBase+"/members/"+url.PathEscape(subject), nil); err != nil {
			return fmt.Errorf("remove organization member: %w", err)
		}
	default:
		return fmt.Errorf("unknown membership action %q", step.action.Action)
	}
	return nil
}

func verifySubjectMemberships(state DesiredState, current subjectMemberships, desired Memberships) error {
	desiredByTenant := make(map[string]MembershipClass, len(desired.Memberships))
	for _, membership := range desired.Memberships {
		desiredByTenant[membership.Tenant] = membership.Class
	}
	for _, tenant := range state.tenantCatalog.All() {
		tenantID := string(tenant.ID)
		wantedClass, wanted := desiredByTenant[tenantID]
		actual := current[tenantID]
		if !wanted && actual.member {
			return fmt.Errorf("%w: subject remains a member of tenant %s", ErrMembershipVerification, tenantID)
		}
		if wanted && (!actual.member || !exactMembershipClass(actual.classes, wantedClass)) {
			return fmt.Errorf("%w: subject does not have exact class %s in tenant %s", ErrMembershipVerification, wantedClass, tenantID)
		}
	}
	return nil
}

func validateCanonicalSubject(subject string) error {
	parsed, err := uuid.Parse(subject)
	if err != nil || parsed == uuid.Nil || parsed.String() != subject {
		return ErrInvalidMemberships
	}
	return nil
}
