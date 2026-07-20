package keycloakadmin

import (
	"context"
	"fmt"
	"net/url"
	"sort"
)

const configureTOTPProvider = "CONFIGURE_TOTP"

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
	browser := managedAuthenticationFlow{
		Alias:       state.Authentication.BrowserFlow,
		Description: "Noebs Google-only browser authentication",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "auth-cookie", Requirement: "ALTERNATIVE", Priority: 10},
			{
				ProviderID: "identity-provider-redirector", Requirement: "ALTERNATIVE", Priority: 20,
				ConfigAlias: "noebs-google-redirect", Config: map[string]string{"defaultProvider": "google"},
			},
		},
	}
	firstBroker := managedAuthenticationFlow{
		Alias:       state.Authentication.FirstBrokerLoginFlow,
		Description: "Create a unique user from Google; existing local identities fail closed",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "idp-create-user-if-unique", Requirement: "REQUIRED", Priority: 10},
		},
	}
	postBroker := managedAuthenticationFlow{
		Alias:       state.Authentication.PostBrokerLoginFlow,
		Description: "Mandatory OTP after every Google login",
		Executions: []managedAuthenticationExecution{
			{ProviderID: "auth-otp-form", Requirement: "REQUIRED", Priority: 10},
		},
	}
	return []managedAuthenticationFlow{browser, firstBroker, postBroker}
}

func reconcileHumanAuthentication(ctx context.Context, session *adminSession, state DesiredState, result *Result) error {
	flows := desiredAuthenticationFlows(state)
	for index := range flows {
		if err := reconcileTopLevelAuthenticationFlow(ctx, session, state.Realm.Name, flows[index], result); err != nil {
			return err
		}
	}
	return reconcileOTPRequiredAction(ctx, session, state, result)
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
		Alias: desired.Alias, Description: desired.Description, ProviderID: "basic-flow", TopLevel: true, BuiltIn: false,
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
		"provider": "basic-flow", "type": "basic-flow", "priority": desired.Priority,
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
		ID: current.ID, Alias: desired.Alias, Description: desired.Description, ProviderID: "basic-flow", TopLevel: false, BuiltIn: false,
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
		return current.AuthenticationFlow && current.DisplayName == desired.Flow.Alias
	}
	return !current.AuthenticationFlow && current.ProviderID == desired.ProviderID
}

func reconcileAuthenticatorConfig(ctx context.Context, session *adminSession, base string, current *authenticationExecutionInfoRepresentation, desired managedAuthenticationExecution, result *Result) error {
	if desired.Flow != nil {
		return nil
	}
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

func reconcileOTPRequiredAction(ctx context.Context, session *adminSession, state DesiredState, result *Result) error {
	base := realmPath(state.Realm.Name) + "/authentication"
	var actions []requiredActionProviderRepresentation
	if _, err := session.get(ctx, base+"/required-actions", &actions); err != nil {
		return fmt.Errorf("list required actions: %w", err)
	}
	action := state.Authentication.OTP.ConfigureRequiredAction
	wanted := requiredActionProviderRepresentation{
		Alias: action.Alias, Name: "Configure OTP", ProviderID: configureTOTPProvider,
		Enabled: action.Enabled, DefaultAction: action.DefaultAction, Priority: action.Priority, Config: map[string]string{},
	}
	found := false
	for _, current := range actions {
		if current.Alias == wanted.Alias {
			found = true
			if !requiredActionMatches(current, wanted) {
				if err := session.put(ctx, base+"/required-actions/"+url.PathEscape(current.Alias), wanted); err != nil {
					return fmt.Errorf("update Configure OTP required action: %w", err)
				}
				result.Updated++
			}
			continue
		}
		if current.Enabled || current.DefaultAction {
			current.Enabled = false
			current.DefaultAction = false
			if err := session.put(ctx, base+"/required-actions/"+url.PathEscape(current.Alias), current); err != nil {
				return fmt.Errorf("disable required action %s outside desired state: %w", current.Alias, err)
			}
			result.Updated++
		}
	}
	if found {
		return nil
	}
	if err := session.post(ctx, base+"/register-required-action", map[string]string{"providerId": configureTOTPProvider, "name": "Configure OTP"}); err != nil {
		return fmt.Errorf("register Configure OTP required action: %w", err)
	}
	result.Created++
	if err := session.put(ctx, base+"/required-actions/"+url.PathEscape(wanted.Alias), wanted); err != nil {
		return fmt.Errorf("configure registered Configure OTP required action: %w", err)
	}
	result.Updated++
	return nil
}

func requiredActionMatches(current, desired requiredActionProviderRepresentation) bool {
	return current.Alias == desired.Alias && current.Name == desired.Name && current.ProviderID == desired.ProviderID &&
		current.Enabled == desired.Enabled && current.DefaultAction == desired.DefaultAction &&
		current.Priority == desired.Priority && equalStringMap(current.Config, desired.Config)
}
