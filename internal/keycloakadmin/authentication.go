package keycloakadmin

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

const (
	configureTOTPProvider         = "CONFIGURE_TOTP"
	registrationFlowAlias         = "noebs-registration"
	registrationFormFlowAlias     = "noebs-registration-form"
	resetCredentialsFlowAlias     = "noebs-reset-credentials"
	primaryCredentialsFlowAlias   = "noebs-primary-credentials"
	authenticationLevelsFlowAlias = "noebs-authentication-levels"
	primaryLoA1FlowAlias          = "noebs-primary-loa1"
	mfaLoA2FlowAlias              = "noebs-mfa-loa2"
	postBrokerLoA1FlowAlias       = "noebs-post-broker-loa1"
	postBrokerLoA2FlowAlias       = "noebs-post-broker-loa2"
)

type authenticationFlowRepresentation struct {
	ID          string `json:"id,omitempty"`
	Alias       string `json:"alias"`
	Description string `json:"description"`
	ProviderID  string `json:"providerId"`
	TopLevel    bool   `json:"topLevel"`
	BuiltIn     bool   `json:"builtIn"`
}

type authenticationExecutionInfoRepresentation struct {
	ID                   string   `json:"id"`
	Requirement          string   `json:"requirement"`
	DisplayName          string   `json:"displayName"`
	Alias                string   `json:"alias"`
	Description          string   `json:"description"`
	RequirementChoices   []string `json:"requirementChoices"`
	Configurable         bool     `json:"configurable"`
	AuthenticationFlow   bool     `json:"authenticationFlow"`
	ProviderID           string   `json:"providerId"`
	AuthenticationConfig string   `json:"authenticationConfig"`
	FlowID               string   `json:"flowId"`
	Level                int      `json:"level"`
	Index                int      `json:"index"`
	Priority             int      `json:"priority"`
}

type requiredActionProviderRepresentation struct {
	Alias         string            `json:"alias"`
	Name          string            `json:"name"`
	ProviderID    string            `json:"providerId"`
	Enabled       bool              `json:"enabled"`
	DefaultAction bool              `json:"defaultAction"`
	Priority      int               `json:"priority"`
	Config        map[string]string `json:"config"`
}

type authenticatorConfigRepresentation struct {
	ID     string            `json:"id,omitempty"`
	Alias  string            `json:"alias"`
	Config map[string]string `json:"config"`
}

type managedAuthenticationFlow struct {
	ProviderID  string
	Alias       string
	Description string
	Executions  []managedAuthenticationExecution
}

type managedAuthenticationExecution struct {
	ProviderID  string
	Requirement string
	Priority    int
	ConfigAlias string
	Config      map[string]string
	Flow        *managedAuthenticationFlow
}

func desiredAuthenticationFlows(state DesiredState) []managedAuthenticationFlow {
	loa1 := state.Authentication.Levels[0]
	loa2 := state.Authentication.Levels[1]
	// Keep alternatives in their own required subflow: Keycloak ignores
	// alternatives that share a level with a required execution.
	credentialsFlow := managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       primaryCredentialsFlowAlias,
		Description: "Local credentials or an explicitly selected identity provider",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "identity-provider-redirector", Requirement: "ALTERNATIVE", Priority: 10},
			{ProviderID: "auth-username-password-form", Requirement: "ALTERNATIVE", Priority: 20},
		},
	}
	loa1Flow := authenticationLevelFlow(
		primaryLoA1FlowAlias,
		"Password or federated authentication establishes reusable LoA1",
		loa1,
		managedAuthenticationExecution{Requirement: "REQUIRED", Priority: 20, Flow: &credentialsFlow},
	)
	loa2Flow := authenticationLevelFlow(
		mfaLoA2FlowAlias,
		"TOTP establishes LoA2 for the current authorization only",
		loa2,
		managedAuthenticationExecution{ProviderID: "auth-otp-form", Requirement: "REQUIRED", Priority: 20},
	)
	levelsFlow := managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       authenticationLevelsFlowAlias,
		Description: "Noebs ordered levels of authentication",
		Executions: []managedAuthenticationExecution{
			{Requirement: "CONDITIONAL", Priority: 10, Flow: &loa1Flow},
			{Requirement: "CONDITIONAL", Priority: 20, Flow: &loa2Flow},
			// For organization-scoped SSO, Keycloak's cookie authenticator
			// attaches the verified user but reports ATTEMPTED. Both level
			// flows can then be skipped because assurance is already met.
			// Complete only after the required levels; the processor still
			// rejects a flow without an authenticated user.
			{ProviderID: "allow-access-authenticator", Requirement: "REQUIRED", Priority: 30},
		},
	}
	browser := managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       state.Authentication.BrowserFlow,
		Description: "Reusable primary authentication with one-request TOTP LoA2 step-up",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "auth-cookie", Requirement: "ALTERNATIVE", Priority: 10},
			{Requirement: "ALTERNATIVE", Priority: 20, Flow: &levelsFlow},
		},
	}
	firstBroker := managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       state.Authentication.FirstBrokerLoginFlow,
		Description: "Validate the federated profile and create a unique user; existing identities are never linked automatically",
		Executions: []managedAuthenticationExecution{
			{
				ProviderID: "idp-review-profile", Requirement: "REQUIRED", Priority: 10,
				ConfigAlias: "noebs-first-broker-profile", Config: map[string]string{"update.profile.on.first.login": "missing"},
			},
			{ProviderID: "idp-create-user-if-unique", Requirement: "REQUIRED", Priority: 20},
		},
	}
	postBroker := managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       state.Authentication.PostBrokerLoginFlow,
		Description: "Establish requested Noebs authentication levels after federation",
		Executions:  completedPrimaryAuthenticationExecutions(state.Authentication.PostBrokerLoginFlow, 10, loa1, loa2),
	}
	registrationForm := managedAuthenticationFlow{
		ProviderID:  "form-flow",
		Alias:       registrationFormFlowAlias,
		Description: "Create a local account using the realm user profile and password policy",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "registration-user-creation", Requirement: "REQUIRED", Priority: 10},
			{ProviderID: "registration-password-action", Requirement: "REQUIRED", Priority: 20},
		},
	}
	registration := managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       registrationFlowAlias,
		Description: "Keycloak local account registration",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "registration-page-form", Requirement: "REQUIRED", Priority: 10, Flow: &registrationForm},
		},
	}
	registration.Executions = append(registration.Executions, completedPrimaryAuthenticationExecutions(registration.Alias, 20, loa1, loa2)...)
	resetCredentials := managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       resetCredentialsFlowAlias,
		Description: "Reset a password through email while preserving enrolled second factors",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "reset-credentials-choose-user", Requirement: "REQUIRED", Priority: 10},
			{ProviderID: "reset-credential-email", Requirement: "REQUIRED", Priority: 20},
			{ProviderID: "reset-password", Requirement: "REQUIRED", Priority: 30},
		},
	}
	resetCredentials.Executions = append(resetCredentials.Executions, completedPrimaryAuthenticationExecutions(resetCredentials.Alias, 40, loa1, loa2)...)
	return []managedAuthenticationFlow{browser, firstBroker, postBroker, registration, resetCredentials}
}

// Registration, password recovery, and federation complete outside the browser
// flow. Each must establish the same primary level and satisfy a requested MFA
// level before it can issue a token. The preceding executions prove the primary
// identity; this helper never bypasses the required second factor.
func completedPrimaryAuthenticationExecutions(alias string, priority int, loa1, loa2 AuthenticationLevel) []managedAuthenticationExecution {
	primary := authenticationLevelFlow(
		alias+"-loa1", "Completed primary authentication establishes reusable LoA1", loa1,
		managedAuthenticationExecution{ProviderID: "allow-access-authenticator", Requirement: "REQUIRED", Priority: 20},
	)
	mfa := authenticationLevelFlow(
		alias+"-loa2", "TOTP establishes LoA2 for the current authorization only", loa2,
		managedAuthenticationExecution{ProviderID: "auth-otp-form", Requirement: "REQUIRED", Priority: 20},
	)
	return []managedAuthenticationExecution{
		{Requirement: "CONDITIONAL", Priority: priority, Flow: &primary},
		{Requirement: "CONDITIONAL", Priority: priority + 10, Flow: &mfa},
	}
}

func authenticationLevelFlow(alias, description string, level AuthenticationLevel, authenticator managedAuthenticationExecution) managedAuthenticationFlow {
	return managedAuthenticationFlow{
		ProviderID:  "basic-flow",
		Alias:       alias,
		Description: description,
		Executions: []managedAuthenticationExecution{
			{
				ProviderID: "conditional-level-of-authentication", Requirement: "REQUIRED", Priority: 10,
				ConfigAlias: alias,
				Config: map[string]string{
					"loa-condition-level": strconv.Itoa(level.Level),
					"loa-max-age":         strconv.Itoa(level.MaxAgeSeconds),
				},
			},
			authenticator,
		},
	}
}

func reconcileHumanAuthentication(ctx context.Context, session *adminSession, state DesiredState, result *Result) error {
	flows := desiredAuthenticationFlows(state)
	for index := range flows {
		if err := reconcileTopLevelAuthenticationFlow(ctx, session, state.Realm.Name, flows[index], result); err != nil {
			return err
		}
	}
	return reconcileRequiredActions(ctx, session, state, result)
}

func reconcileTopLevelAuthenticationFlow(ctx context.Context, session *adminSession, realm string, desired managedAuthenticationFlow, result *Result) error {
	base := realmPath(realm) + "/authentication"
	flows, err := listAuthenticationFlows(ctx, session, base)
	if err != nil {
		return err
	}
	var current authenticationFlowRepresentation
	for _, flow := range flows {
		if flow.Alias == desired.Alias {
			current = flow
			break
		}
	}
	wanted := authenticationFlowRepresentation{
		Alias: desired.Alias, Description: desired.Description, ProviderID: desired.ProviderID, TopLevel: true, BuiltIn: false,
	}
	if current.ID == "" {
		if err := session.post(ctx, base+"/flows", wanted); err != nil {
			return fmt.Errorf("create authentication flow %s: %w", desired.Alias, err)
		}
		result.Created++
	} else {
		if current.BuiltIn || !current.TopLevel {
			return fmt.Errorf("%w: managed authentication flow %s has an invalid type", ErrUnexpectedResponse, desired.Alias)
		}
		wanted.ID = current.ID
		if !authenticationFlowMatches(current, wanted) {
			if err := session.put(ctx, base+"/flows/"+url.PathEscape(current.ID), wanted); err != nil {
				return fmt.Errorf("update authentication flow %s: %w", desired.Alias, err)
			}
			result.Updated++
		}
	}
	return reconcileAuthenticationExecutions(ctx, session, base, desired, result)
}

func reconcileAuthenticationExecutions(ctx context.Context, session *adminSession, base string, desired managedAuthenticationFlow, result *Result) error {
	current, err := listDirectAuthenticationExecutions(ctx, session, base, desired.Alias)
	if err != nil {
		return err
	}
	used := make([]bool, len(current))
	matches := make([]int, len(desired.Executions))
	for index := range matches {
		matches[index] = -1
	}
	for wantedIndex, wanted := range desired.Executions {
		for currentIndex, execution := range current {
			if used[currentIndex] || !authenticationExecutionIdentityMatches(execution, wanted) {
				continue
			}
			used[currentIndex] = true
			matches[wantedIndex] = currentIndex
			break
		}
	}
	for index := len(current) - 1; index >= 0; index-- {
		if used[index] {
			continue
		}
		if err := session.delete(ctx, base+"/executions/"+url.PathEscape(current[index].ID), nil); err != nil {
			return fmt.Errorf("delete execution outside authentication flow %s: %w", desired.Alias, err)
		}
		result.Deleted++
	}
	for wantedIndex, wanted := range desired.Executions {
		var execution authenticationExecutionInfoRepresentation
		if matches[wantedIndex] == -1 {
			if err := createAuthenticationExecution(ctx, session, base, desired.Alias, wanted); err != nil {
				return err
			}
			result.Created++
			reloaded, err := listDirectAuthenticationExecutions(ctx, session, base, desired.Alias)
			if err != nil {
				return err
			}
			for _, candidate := range reloaded {
				if authenticationExecutionIdentityMatches(candidate, wanted) {
					execution = candidate
					break
				}
			}
			if execution.ID == "" {
				return fmt.Errorf("%w: created execution was not returned for authentication flow %s", ErrUnexpectedResponse, desired.Alias)
			}
		} else {
			execution = current[matches[wantedIndex]]
		}
		if execution.Requirement != wanted.Requirement || execution.Priority != wanted.Priority {
			execution.Requirement = wanted.Requirement
			execution.Priority = wanted.Priority
			path := base + "/flows/" + url.PathEscape(desired.Alias) + "/executions"
			if err := session.put(ctx, path, execution); err != nil {
				return fmt.Errorf("update execution in authentication flow %s: %w", desired.Alias, err)
			}
			result.Updated++
		}
		if err := reconcileAuthenticatorConfig(ctx, session, base, &execution, wanted, result); err != nil {
			return err
		}
		if wanted.Flow != nil {
			if err := reconcileSubflowMetadata(ctx, session, base, execution, *wanted.Flow, result); err != nil {
				return err
			}
			if err := reconcileAuthenticationExecutions(ctx, session, base, *wanted.Flow, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func createAuthenticationExecution(ctx context.Context, session *adminSession, base, parentAlias string, desired managedAuthenticationExecution) error {
	path := base + "/flows/" + url.PathEscape(parentAlias) + "/executions/"
	if desired.Flow == nil {
		return session.post(ctx, path+"execution", map[string]any{"provider": desired.ProviderID, "priority": desired.Priority})
	}
	return session.post(ctx, path+"flow", map[string]any{
		"alias": desired.Flow.Alias, "description": desired.Flow.Description,
		"provider": desired.ProviderID, "type": desired.Flow.ProviderID, "priority": desired.Priority,
	})
}

func reconcileSubflowMetadata(ctx context.Context, session *adminSession, base string, execution authenticationExecutionInfoRepresentation, desired managedAuthenticationFlow, result *Result) error {
	if execution.FlowID == "" {
		return fmt.Errorf("%w: subflow %s has no flow id", ErrUnexpectedResponse, desired.Alias)
	}
	var current authenticationFlowRepresentation
	found, err := session.get(ctx, base+"/flows/"+url.PathEscape(execution.FlowID), &current)
	if err != nil {
		return fmt.Errorf("read authentication subflow %s: %w", desired.Alias, err)
	}
	if !found {
		return fmt.Errorf("%w: authentication subflow %s does not exist", ErrUnexpectedResponse, desired.Alias)
	}
	wanted := authenticationFlowRepresentation{
		ID: current.ID, Alias: desired.Alias, Description: desired.Description, ProviderID: desired.ProviderID, TopLevel: false, BuiltIn: false,
	}
	if authenticationFlowMatches(current, wanted) {
		return nil
	}
	if current.BuiltIn || current.TopLevel {
		return fmt.Errorf("%w: managed authentication subflow %s has an invalid type", ErrUnexpectedResponse, desired.Alias)
	}
	if err := session.put(ctx, base+"/flows/"+url.PathEscape(current.ID), wanted); err != nil {
		return fmt.Errorf("update authentication subflow %s: %w", desired.Alias, err)
	}
	result.Updated++
	return nil
}

func listAuthenticationFlows(ctx context.Context, session *adminSession, base string) ([]authenticationFlowRepresentation, error) {
	var flows []authenticationFlowRepresentation
	if _, err := session.get(ctx, base+"/flows", &flows); err != nil {
		return nil, fmt.Errorf("list authentication flows: %w", err)
	}
	return flows, nil
}

func listDirectAuthenticationExecutions(ctx context.Context, session *adminSession, base, alias string) ([]authenticationExecutionInfoRepresentation, error) {
	var executions []authenticationExecutionInfoRepresentation
	path := base + "/flows/" + url.PathEscape(alias) + "/executions"
	if _, err := session.get(ctx, path, &executions); err != nil {
		return nil, fmt.Errorf("list executions for authentication flow %s: %w", alias, err)
	}
	direct := executions[:0]
	for _, execution := range executions {
		if execution.Level == 0 {
			direct = append(direct, execution)
		}
	}
	sort.Slice(direct, func(i, j int) bool {
		if direct[i].Priority == direct[j].Priority {
			return direct[i].ID < direct[j].ID
		}
		return direct[i].Priority < direct[j].Priority
	})
	return direct, nil
}

func authenticationExecutionIdentityMatches(current authenticationExecutionInfoRepresentation, desired managedAuthenticationExecution) bool {
	if desired.Flow != nil {
		return current.AuthenticationFlow && current.DisplayName == desired.Flow.Alias && current.ProviderID == desired.ProviderID
	}
	return !current.AuthenticationFlow && current.ProviderID == desired.ProviderID
}

func reconcileAuthenticatorConfig(ctx context.Context, session *adminSession, base string, current *authenticationExecutionInfoRepresentation, desired managedAuthenticationExecution, result *Result) error {
	if desired.Config == nil {
		if current.AuthenticationConfig == "" {
			return nil
		}
		if err := session.delete(ctx, base+"/config/"+url.PathEscape(current.AuthenticationConfig), nil); err != nil {
			return fmt.Errorf("delete unexpected authenticator config for %s: %w", desired.ProviderID, err)
		}
		current.AuthenticationConfig = ""
		result.Deleted++
		return nil
	}
	wanted := authenticatorConfigRepresentation{Alias: desired.ConfigAlias, Config: cloneStringMap(desired.Config)}
	if current.AuthenticationConfig == "" {
		if err := session.post(ctx, base+"/executions/"+url.PathEscape(current.ID)+"/config", wanted); err != nil {
			return fmt.Errorf("create authenticator config for %s: %w", desired.ProviderID, err)
		}
		result.Created++
		return nil
	}
	var existing authenticatorConfigRepresentation
	found, err := session.get(ctx, base+"/config/"+url.PathEscape(current.AuthenticationConfig), &existing)
	if err != nil {
		return fmt.Errorf("read authenticator config for %s: %w", desired.ProviderID, err)
	}
	if !found {
		return fmt.Errorf("%w: authenticator config for %s does not exist", ErrUnexpectedResponse, desired.ProviderID)
	}
	wanted.ID = existing.ID
	if existing.Alias == wanted.Alias && equalStringMap(existing.Config, wanted.Config) {
		return nil
	}
	if err := session.put(ctx, base+"/config/"+url.PathEscape(existing.ID), wanted); err != nil {
		return fmt.Errorf("update authenticator config for %s: %w", desired.ProviderID, err)
	}
	result.Updated++
	return nil
}

func authenticationFlowMatches(current, desired authenticationFlowRepresentation) bool {
	return current.Alias == desired.Alias && current.Description == desired.Description &&
		current.ProviderID == desired.ProviderID && current.TopLevel == desired.TopLevel && current.BuiltIn == desired.BuiltIn
}

func pruneUnmanagedAuthenticationFlows(ctx context.Context, session *adminSession, realm string, desired []managedAuthenticationFlow, result *Result) error {
	base := realmPath(realm) + "/authentication"
	flows, err := listAuthenticationFlows(ctx, session, base)
	if err != nil {
		return err
	}
	keep := make(map[string]struct{}, len(desired))
	for _, flow := range desired {
		keep[flow.Alias] = struct{}{}
	}
	for _, flow := range flows {
		if flow.BuiltIn || !flow.TopLevel {
			continue
		}
		if _, found := keep[flow.Alias]; found {
			continue
		}
		if err := session.delete(ctx, base+"/flows/"+url.PathEscape(flow.ID), nil); err != nil {
			return fmt.Errorf("delete authentication flow %s outside desired state: %w", flow.Alias, err)
		}
		result.Deleted++
	}
	return nil
}

// Required actions remain opt-in unless the realm policy or an authenticator
// requests them. Enabling password/profile maintenance does not require a
// password for federated users, and email verification skips users without email.
// Verify email before credential enrollment so a pending registration cannot
// bind an attacker's password or second factor to someone else's mailbox.
func desiredRequiredActions(state DesiredState) []requiredActionProviderRepresentation {
	action := state.Authentication.OTP.ConfigureRequiredAction
	return []requiredActionProviderRepresentation{
		{
			Alias: action.Alias, Name: "Configure OTP", ProviderID: configureTOTPProvider,
			Enabled: action.Enabled, DefaultAction: action.DefaultAction, Priority: action.Priority, Config: map[string]string{},
		},
		{Alias: "UPDATE_PASSWORD", Name: "Update Password", ProviderID: "UPDATE_PASSWORD", Enabled: true, Priority: 57, Config: map[string]string{}},
		{Alias: "UPDATE_PROFILE", Name: "Update Profile", ProviderID: "UPDATE_PROFILE", Enabled: true, Priority: 40, Config: map[string]string{}},
		{Alias: "VERIFY_EMAIL", Name: "Verify Email", ProviderID: "VERIFY_EMAIL", Enabled: true, Priority: 0, Config: map[string]string{}},
		{Alias: "VERIFY_PROFILE", Name: "Verify Profile", ProviderID: "VERIFY_PROFILE", Enabled: true, Priority: 100, Config: map[string]string{}},
	}
}

func reconcileRequiredActions(ctx context.Context, session *adminSession, state DesiredState, result *Result) error {
	base := realmPath(state.Realm.Name) + "/authentication"
	var actions []requiredActionProviderRepresentation
	if _, err := session.get(ctx, base+"/required-actions", &actions); err != nil {
		return fmt.Errorf("list required actions: %w", err)
	}
	desired := desiredRequiredActions(state)
	wantedByAlias := make(map[string]requiredActionProviderRepresentation, len(desired))
	for _, action := range desired {
		wantedByAlias[action.Alias] = action
	}
	found := make(map[string]bool, len(desired))
	for _, current := range actions {
		wanted, managed := wantedByAlias[current.Alias]
		if managed {
			found[current.Alias] = true
			if requiredActionMatches(current, wanted) {
				continue
			}
		} else {
			if !current.Enabled && !current.DefaultAction {
				continue
			}
			wanted = current
			wanted.Enabled = false
			wanted.DefaultAction = false
		}
		if err := session.put(ctx, base+"/required-actions/"+url.PathEscape(current.Alias), wanted); err != nil {
			return fmt.Errorf("reconcile required action %s: %w", current.Alias, err)
		}
		result.Updated++
	}
	for _, wanted := range desired {
		if found[wanted.Alias] {
			continue
		}
		if err := session.post(ctx, base+"/register-required-action", map[string]string{"providerId": wanted.ProviderID, "name": wanted.Name}); err != nil {
			return fmt.Errorf("register required action %s: %w", wanted.Alias, err)
		}
		result.Created++
		if err := session.put(ctx, base+"/required-actions/"+url.PathEscape(wanted.Alias), wanted); err != nil {
			return fmt.Errorf("configure registered required action %s: %w", wanted.Alias, err)
		}
		result.Updated++
	}
	return nil
}

func requiredActionMatches(current, desired requiredActionProviderRepresentation) bool {
	return current.Alias == desired.Alias && current.Name == desired.Name && current.ProviderID == desired.ProviderID &&
		current.Enabled == desired.Enabled && current.DefaultAction == desired.DefaultAction &&
		current.Priority == desired.Priority && equalStringMap(current.Config, desired.Config)
}
