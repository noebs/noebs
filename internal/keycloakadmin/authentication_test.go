package keycloakadmin

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"
)

func authenticationTestState() DesiredState {
	return DesiredState{
		Realm: Realm{Name: "noebs"},
		Authentication: Authentication{
			BrowserFlow: "noebs-browser", FirstBrokerLoginFlow: "noebs-first-broker-login", PostBrokerLoginFlow: "noebs-post-broker",
			Levels: []AuthenticationLevel{
				{ACR: primaryACR, Level: 1, MaxAgeSeconds: 28800},
				{ACR: mfaACR, Level: 2, MaxAgeSeconds: 0},
			},
			OTP: OTPPolicy{ConfigureRequiredAction: RequiredAction{Alias: configureTOTPProvider, Enabled: true, Priority: 10}},
		},
	}
}

func TestPrimaryAuthenticationSupportsLocalCredentialsWithoutIdentityProviders(t *testing.T) {
	state := authenticationTestState()
	flows := desiredAuthenticationFlows(state)
	browser := findManagedFlow(t, flows, state.Authentication.BrowserFlow)
	if len(browser.Executions) != 2 || browser.Executions[0].ProviderID != "auth-cookie" || browser.Executions[0].Requirement != "ALTERNATIVE" {
		t.Fatalf("browser must retain reusable session authentication: %#v", browser.Executions)
	}
	levels := browser.Executions[1]
	if levels.Requirement != "ALTERNATIVE" || levels.Flow == nil || levels.Flow.Alias != authenticationLevelsFlowAlias {
		t.Fatalf("browser credential path = %#v", levels)
	}
	if len(levels.Flow.Executions) != 3 {
		t.Fatalf("authentication levels = %#v", levels.Flow.Executions)
	}
	primary := levels.Flow.Executions[0]
	mfa := levels.Flow.Executions[1]
	assertAuthenticationLevel(t, primary, 1, "28800")
	assertAuthenticationLevel(t, mfa, 2, "0")
	credentialPath := primary.Flow.Executions[1]
	if credentialPath.Requirement != "REQUIRED" || credentialPath.Flow == nil || credentialPath.Flow.Alias != primaryCredentialsFlowAlias {
		t.Fatalf("credentials must be required inside the primary level: %#v", credentialPath)
	}
	credentials := credentialPath.Flow.Executions
	if len(credentials) != 2 || credentials[0].ProviderID != "identity-provider-redirector" || credentials[1].ProviderID != "auth-username-password-form" {
		t.Fatalf("primary credential choices = %#v", credentials)
	}
	for _, execution := range credentials {
		if execution.Requirement != "ALTERNATIVE" || execution.Config != nil {
			t.Fatalf("credential choice must not force a provider or bypass another choice: %#v", execution)
		}
	}
	assertRequiredTOTP(t, mfa)
	completion := levels.Flow.Executions[2]
	if completion.ProviderID != "allow-access-authenticator" || completion.Requirement != "REQUIRED" || completion.Priority <= mfa.Priority {
		t.Fatalf("organization SSO must finish only after all required assurance levels: %#v", completion)
	}
}

func TestFirstBrokerLoginValidatesProfileAndRepairsLinkingDrift(t *testing.T) {
	state := authenticationTestState()
	broker := findManagedFlow(t, desiredAuthenticationFlows(state), state.Authentication.FirstBrokerLoginFlow)
	if len(broker.Executions) != 2 {
		t.Fatalf("first broker flow must contain profile review and unique creation only: %#v", broker.Executions)
	}
	for index, provider := range []string{"idp-review-profile", "idp-create-user-if-unique"} {
		execution := broker.Executions[index]
		if execution.ProviderID != provider || execution.Requirement != "REQUIRED" || execution.Flow != nil {
			t.Fatalf("broker profile validation must precede required unique creation with no linking alternatives: %#v", broker.Executions)
		}
	}
	if broker.Executions[0].Priority >= broker.Executions[1].Priority || !reflect.DeepEqual(broker.Executions[0].Config, map[string]string{"update.profile.on.first.login": "missing"}) {
		t.Fatalf("broker profile review must run before creation only when validation requires it: %#v", broker.Executions)
	}
	fake := newFakeKeycloak()
	fake.realm = &realmRepresentation{Realm: state.Realm.Name}
	server := httptest.NewServer(fake)
	defer server.Close()
	session := &adminSession{baseURL: server.URL, token: "admin-token", http: server.Client()}
	var initial Result
	if err := reconcileTopLevelAuthenticationFlow(context.Background(), session, state.Realm.Name, broker, &initial); err != nil {
		t.Fatal(err)
	}
	flowID := fake.authenticationFlowIDByAlias(broker.Alias)
	review := &fake.authenticationExecutions[flowID][0]
	review.Requirement = "DISABLED"
	fake.authenticatorConfigs[review.AuthenticationConfig] = authenticatorConfigRepresentation{
		ID: review.AuthenticationConfig, Alias: "hostile-review", Config: map[string]string{"update.profile.on.first.login": "off"},
	}
	fake.authenticationExecutions[flowID] = append(fake.authenticationExecutions[flowID], authenticationExecutionInfoRepresentation{
		ID: fake.id("hostile-link"), ProviderID: "idp-auto-link", Requirement: "ALTERNATIVE", Priority: 30,
	})
	var repaired Result
	if err := reconcileTopLevelAuthenticationFlow(context.Background(), session, state.Realm.Name, broker, &repaired); err != nil {
		t.Fatal(err)
	}
	if repaired.Deleted != 1 || repaired.Updated == 0 {
		t.Fatalf("broker drift repair = %#v", repaired)
	}
	assertManagedAuthenticationFlow(t, fake, broker)
	var final Result
	if err := reconcileTopLevelAuthenticationFlow(context.Background(), session, state.Realm.Name, broker, &final); err != nil {
		t.Fatal(err)
	}
	if final.Changed() {
		t.Fatalf("broker flow did not converge after repair: %#v", final)
	}
}

func TestRegistrationAndRecoveryHonorRequestedAssuranceWithoutRemovingOTP(t *testing.T) {
	state := authenticationTestState()
	flows := desiredAuthenticationFlows(state)
	registration := findManagedFlow(t, flows, registrationFlowAlias)
	if len(registration.Executions) != 3 {
		t.Fatalf("registration executions = %#v", registration.Executions)
	}
	page := registration.Executions[0]
	if page.ProviderID != "registration-page-form" || page.Requirement != "REQUIRED" || page.Flow == nil || page.Flow.ProviderID != "form-flow" {
		t.Fatalf("registration must use the native Keycloak form: %#v", page)
	}
	if len(page.Flow.Executions) != 2 || page.Flow.Executions[0].ProviderID != "registration-user-creation" || page.Flow.Executions[1].ProviderID != "registration-password-action" {
		t.Fatalf("registration must validate the user profile and password: %#v", page.Flow.Executions)
	}
	for _, execution := range page.Flow.Executions {
		if execution.Requirement != "REQUIRED" {
			t.Fatalf("registration validation cannot be optional: %#v", execution)
		}
	}
	reset := findManagedFlow(t, flows, resetCredentialsFlowAlias)
	if len(reset.Executions) != 5 {
		t.Fatalf("password recovery executions = %#v", reset.Executions)
	}
	for i, provider := range []string{"reset-credentials-choose-user", "reset-credential-email", "reset-password"} {
		if reset.Executions[i].ProviderID != provider || reset.Executions[i].Requirement != "REQUIRED" {
			t.Fatalf("password recovery must verify the email before resetting a password: %#v", reset.Executions)
		}
	}
	for _, flow := range []managedAuthenticationFlow{registration, reset, findManagedFlow(t, flows, state.Authentication.PostBrokerLoginFlow)} {
		primary := flow.Executions[len(flow.Executions)-2]
		mfa := flow.Executions[len(flow.Executions)-1]
		assertAuthenticationLevel(t, primary, 1, "28800")
		assertAuthenticationLevel(t, mfa, 2, "0")
		if primary.Flow.Executions[1].ProviderID != "allow-access-authenticator" || primary.Flow.Executions[1].Requirement != "REQUIRED" {
			t.Fatalf("completed primary authentication = %#v", primary)
		}
		assertRequiredTOTP(t, mfa)
	}
}

func TestRequiredActionsRestoreAccountMaintenanceWithoutForcingPasswordEnrollment(t *testing.T) {
	state := authenticationTestState()
	fake := newFakeKeycloak()
	fake.realm = &realmRepresentation{Realm: state.Realm.Name}
	fake.requiredActions["UPDATE_PASSWORD"] = requiredActionProviderRepresentation{
		Alias: "UPDATE_PASSWORD", Name: "Broken", ProviderID: "UPDATE_PASSWORD", Enabled: false, DefaultAction: true,
	}
	fake.requiredActions["TERMS_AND_CONDITIONS"] = requiredActionProviderRepresentation{
		Alias: "TERMS_AND_CONDITIONS", ProviderID: "TERMS_AND_CONDITIONS", Enabled: true, DefaultAction: true,
	}
	server := httptest.NewServer(fake)
	defer server.Close()
	session := &adminSession{baseURL: server.URL, token: "admin-token", http: server.Client()}
	var result Result
	if err := reconcileRequiredActions(context.Background(), session, state, &result); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{configureTOTPProvider, "UPDATE_PASSWORD", "UPDATE_PROFILE", "VERIFY_EMAIL", "VERIFY_PROFILE"} {
		action, found := fake.requiredActions[alias]
		if !found || !action.Enabled || action.DefaultAction {
			t.Fatalf("account maintenance must remain available without mandatory enrollment: %s = %#v", alias, action)
		}
	}
	for _, alias := range []string{configureTOTPProvider, "UPDATE_PASSWORD"} {
		if fake.requiredActions["VERIFY_EMAIL"].Priority >= fake.requiredActions[alias].Priority {
			t.Fatalf("staged registration must verify email before %s", alias)
		}
	}
	if action := fake.requiredActions["TERMS_AND_CONDITIONS"]; action.Enabled || action.DefaultAction {
		t.Fatalf("unmanaged required action remains enabled: %#v", action)
	}
	var second Result
	if err := reconcileRequiredActions(context.Background(), session, state, &second); err != nil {
		t.Fatal(err)
	}
	if second.Changed() {
		t.Fatalf("required actions did not converge: %#v", second)
	}
}

func findManagedFlow(t *testing.T, flows []managedAuthenticationFlow, alias string) managedAuthenticationFlow {
	t.Helper()
	for _, flow := range flows {
		if flow.Alias == alias {
			return flow
		}
	}
	t.Fatalf("managed flow %s is absent", alias)
	return managedAuthenticationFlow{}
}

func assertAuthenticationLevel(t *testing.T, execution managedAuthenticationExecution, level int, maxAge string) {
	t.Helper()
	if execution.Requirement != "CONDITIONAL" || execution.Flow == nil || len(execution.Flow.Executions) != 2 {
		t.Fatalf("authentication level %d structure = %#v", level, execution)
	}
	condition := execution.Flow.Executions[0]
	levelValue := "1"
	if level == 2 {
		levelValue = "2"
	}
	if condition.ProviderID != "conditional-level-of-authentication" || condition.Requirement != "REQUIRED" || !reflect.DeepEqual(condition.Config, map[string]string{"loa-condition-level": levelValue, "loa-max-age": maxAge}) {
		t.Fatalf("authentication level %d condition = %#v", level, condition)
	}
}

func assertRequiredTOTP(t *testing.T, execution managedAuthenticationExecution) {
	t.Helper()
	otp := execution.Flow.Executions[1]
	if otp.ProviderID != "auth-otp-form" || otp.Requirement != "REQUIRED" || otp.Flow != nil {
		t.Fatalf("MFA requires a fresh TOTP, including for users not yet enrolled: %#v", otp)
	}
}
