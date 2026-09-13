# Local accounts and identity providers

Noebs uses one Keycloak Authorization Code flow with PKCE S256 for human
sign-in. Local credentials and optional social providers produce tokens from one
Keycloak issuer. Each account has its own subject. The gateway, tenant membership policy and
application profile projection are independent of the chosen login method.
Passwords, password resets and OTP secrets never enter the Noebs Go API or
application databases.

## User flow

1. Open the authorization endpoint from the configured Keycloak issuer with
   the mobile client's registered callback, state, nonce and PKCE challenge.
   Leave `kc_idp_hint` unset to show all available choices.
2. Sign in with an email, phone number or username and password, select a
   configured provider, or follow **Register** to create a local account.
3. With email verification enabled, local registration first collects the
   username or international phone handle and optional email/profile. Verify
   the email when supplied, enroll
   the required authenticator, then set the account password. Credentials are
   not enrolled before proving control of the supplied mailbox. A phone-only
   registration skips email verification.
4. Keycloak returns to the same client callback. Exchange the code with its
   PKCE verifier and use the normal bearer token and `X-Active-Tenant` header.
5. The frontend supplies its configured `X-Active-Tenant` to
   `GET /consumer/auth/context` and, when required, `POST /consumer/auth/enrollment`.
   Server policy admits only that selected tenant. Refresh tokens after a grant,
   then create the application profile explicitly through
   `POST /consumer/auth/profile` with the admitted principal. See
   [account enrollment](account-enrollment.md) for the complete contract.

Registration creates a realm identity. It does not assign an organization,
create a wallet, grant a tenant role or create an application profile. The shared
enrollment boundary applies explicit policy after authentication; operators can
also use existing Keycloak membership operations to assign tenant and role by
subject. `lookup-keycloak-subject --username-file <path> --config <path>
--ca <path>` resolves the subject of a phone-only account; the file contains its
exact international username. Use `--email-file` for email lookup. Choose
exactly one selector. The same rule applies to Google, other OIDC providers and local
accounts. Account email addresses and application profile emails are separate
fields with different purposes; profile edits do not change login credentials.

A phone number is stored as the Keycloak username, in canonical international
format such as `+249912345678`. Enter the same format at login; Noebs does not
guess a country code or strip local dialing prefixes. This is a login
identifier with a password, not SMS authentication or proof that the person
controls the phone number. It does not create a `phone_number_verified` claim.
A separate email is optional. Email addresses belong in the email field; an
account handle cannot contain `@`. This keeps email sign-in tied to the verified
contact instead of letting an unverified username claim a mailbox. Email
registration therefore collects a username or phone handle and the email; it is
not an email-only registration form. Email recovery is available only to accounts with
an email address; accounts without one need the operator's account-recovery
process if their password is lost.

## Realm policy and email delivery

The repository-owned policy is in
`infra/kubernetes/keycloak-authority/keycloak-desired-state.yaml`:

```yaml
authentication:
  local_accounts:
    registration_allowed: true
    verify_email: true
    reset_password_allowed: true
    minimum_password_length: 12
```

These settings are reconciled onto Keycloak's native registration, user
profile, password and recovery flows. Password login remains in the shared
browser flow. Brute-force protection and mandatory authenticator enrollment
apply to local accounts too. Noebs does not enable direct password grants or
reintroduce `/consumer/login`, `/consumer/register` or an application token
issuer.

Verification and recovery require explicit SMTP configuration. For Kubernetes
release inputs, set `noebs.keycloak.smtp`; for a reconciler configuration, use
its top-level `smtp` field. Both use the same schema:

```yaml
smtp:
  host: smtp.example.com
  port: 465
  from: accounts@example.com
  from_display_name: Example Wallet
  username: accounts
  password: REPLACE_WITH_SMTP_PASSWORD
  tls: true
```

Use implicit TLS and supply username and password together if authentication
is required. Implicit TLS is required outside loopback test fixtures; the
pinned Keycloak mail sender does not require STARTTLS negotiation, so Noebs
does not offer that downgradeable option. Disabling both verification and recovery permits
omitting SMTP; keeping either enabled without SMTP fails before reconciliation
or release publication. Keycloak sends the messages using the configured
sender and branding.

## Optional providers

The `identity_providers` list in desired state controls which choices appear.
Use `identity_providers: []` for local accounts only. No provider credential is
required in that configuration. With providers enabled, supply exactly their
named credentials in the reconciler's `identity_providers` map, or Kubernetes
release inputs at `noebs.keycloak.identity_providers`:

```yaml
identity_providers:
  company:
    client_id: REPLACE_WITH_CLIENT_ID
    client_secret: REPLACE_WITH_CLIENT_SECRET
```

Missing, incomplete and unused provider credentials fail validation. Google
uses `provider_id: google`. An OIDC provider can use this desired-state entry,
with endpoints taken from its trusted discovery metadata:

```yaml
identity_providers:
  - alias: company
    display_name: Company account
    provider_id: oidc
    credential: company
    config:
      defaultScope: openid profile email
      forwardParameters: login_hint
      syncMode: IMPORT
      issuer: https://identity.example.com
      authorizationUrl: https://identity.example.com/authorize
      tokenUrl: https://identity.example.com/token
      jwksUrl: https://identity.example.com/keys
      userInfoUrl: https://identity.example.com/userinfo
      validateSignature: "true"
      useJwksUrl: "true"
      pkceEnabled: "true"
      pkceMethod: S256
```

Aliases and credential names start with a lowercase letter, contain only
lowercase letters, digits and hyphens, and have at most 63 characters. Register
`<issuer>/broker/<alias>/endpoint` as the provider's callback. The edge allows
only the broker login and callback endpoints for this alias shape; Keycloak
accepts only providers actually configured in the realm. Keycloak's local ACR
values and freshness parameters must not be forwarded to an external provider.
The Google-specific preflight remains useful only when Google is enabled.

First broker login reviews the profile and creates a unique account. An email
match never automatically links an existing local account. Linking identities
requires an explicit, authenticated Keycloak operation.

## Client and white-label contract

Read `GET /app/config` for `oauth.issuer`, `client_id`, `audience`, `scopes` and
`redirect_uri`. The redirect comes from explicit `noebs.mobile_redirect_url`
configuration and must be an HTTPS URL ending in `/mobile/oauth/callback`.
Register that exact URL in the Keycloak mobile client and configure app links
for the brand's domain. Kubernetes release validation requires the callback to
match its configured public origin. The callback is never derived from an
incoming request's Host header.

Use generic **Sign in** and **Create account** language in client applications.
A fixed `kc_idp_hint=google` bypasses the shared selection form, so send an
identity-provider hint only after the user explicitly selects that provider.
The mobile application source is outside this repository.

Normal login requests `urn:noebs:acr:primary` (LoA1). Protected wallet operations
request `urn:noebs:acr:mfa` (LoA2) with `max_age=0`, and both login methods must
perform fresh primary authentication plus TOTP. The transaction authorizer
continues to require the exact ACR, recent `auth_time`, bound subject and
single-use operation authorization. Local accounts do not weaken payment
assurance.

Update the desired state, API configuration, clients and deployment secrets as
one release. Old Google-specific ACR names and top-level `google_client_id` /
`google_client_secret` release input fields are removed; no compatibility path
is provided. Generate a new encrypted release input from
`infra/exe/release.inputs.yaml.example`, including SMTP and the nested provider
map, before preparing and promoting the release. The checked-in production input
now uses `noreply@noebs.sd` through `mail.noebs.sd:465` with implicit TLS and the
nested Google provider credentials. The shared mail server, domain records,
personal inboxes and recovery procedure are managed in
[`infra/mail`](../infra/mail/README.md). Mailbox and application credentials remain
SOPS encrypted; a separate brand supplies its own explicit sender and providers.

## Regression tests

Run `scripts/test-keycloak-accounts.sh` with Java 21, Go, Python 3, curl, tar,
and OpenSSL installed. It downloads the pinned Keycloak 26.7.0 archive,
verifies its checksum, creates an isolated database and HTTPS issuer, and runs
the real authentication tests with Go's race detector. Google is mocked behind
a restricted local proxy and verification/recovery mail is captured locally.
The script stops its processes and removes temporary data when it exits.
`NOEBS_KEYCLOAK_ARCHIVE` can point to the same verified archive for offline reuse;
`NOEBS_KEEP_KEYCLOAK_FIXTURE=1` retains synthetic test data and logs for debugging.

The browser cases cover separate usernames and verified-email login, phone-only
registration, malformed identifier and weak password rejection, verification
before credentials, first-use TOTP, password recovery with the existing second
factor, repeated fresh payment authorization, Google profile review/enrollment,
organization claims, and reconciliation after deliberate configuration drift.
No test uses direct password grants as a substitute for the hosted signup flow.
